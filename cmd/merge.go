package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/google/go-github/v93/github"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/stuttgart-things/claims/internal/ghclient"
)

// claims merge (claims#113): finish the pull request an order, a change or a
// deletion opened -- wait for its checks, then squash-merge it and delete the
// branch.
//
// The merge is the human approval of an order, so claims does not weaken it:
// it merges only PRs on the branches it opens itself (vm-, change-claim/,
// delete-claim/, claim-), only from the repository itself into its default
// branch, never with a failed or (without --wait) unfinished check, and only
// when GitHub reports the PR mergeable without a bypass (mergeable_state
// clean/unstable/has_hooks). A blocked PR -- a review missing, a required
// check not green -- is reported and left open, also for a token that could
// bypass branch protection as an admin.

// mergeBranchPrefixes are the head branches claims opens PRs on: create-vm's
// vm-<name> (render --layout source-path), the other layout orders
// claim-<template>-<name>, change-claim/<name> and delete-claim/<name>.
var mergeBranchPrefixes = []string{"vm-", "change-claim/", "delete-claim/", "claim-"}

// MergeConfig holds the inputs of a merge.
type MergeConfig struct {
	Arg     string        // a claim/VM name, a branch, a PR number or URL
	Repo    string        // owner/name ("" = the checkout's, else the fleet default)
	Wait    bool          // wait for the checks
	Timeout time.Duration // how long --wait waits
	Yes     bool          // do not ask
	DryRun  bool          // show the PR, do not merge
	Token   string        // "" = ghclient.Token's order

	// created: claims opened the PR in this run (--merge on render, change,
	// delete). Its branch may be one --git-branch named.
	created bool
}

var mergeFlags MergeConfig

var mergeCmd = &cobra.Command{
	Use:     "merge <name|branch|pr-number|pr-url>",
	GroupID: groupClaims,
	Short:   "Merge an order, change or deletion PR (squash) after its checks",
	Long: `Merge the pull request claims opened for an order, a change or a deletion.

The PR is a PR number, a PR URL, a branch (vm-<name>, change-claim/<name>,
delete-claim/<name>, claim-<template>-<name>) or a name, which is looked up as
vm-<name>, change-claim/<name> and delete-claim/<name> (a VM's XR name
<name>-vm finds its order vm-<name> too). Only PRs on these branches, from the
repository itself into its default branch, are merged: no arbitrary PRs.

claims shows the title, the labels, the files and the checks, asks (the
title's MERGE = BAU: merging builds the VM; a delete-claim PR deletes the
object), and squash-merges the PR and deletes its branch.

The checks are the required status checks of the base branch, or, when it
requires none, every check on the PR. A failed check stops the merge. Without
--wait a check that still runs stops it too; --wait waits for them (up to
--timeout) and stops at the first failure.

claims never bypasses branch protection: when GitHub reports the PR blocked
(a required review missing, a required check not green) or refuses the merge,
claims prints why and leaves the PR open, even when the token could merge as
an admin.

Talks to the GitHub API (no gh CLI needed): the token is $GIT_TOKEN,
$GITHUB_TOKEN, else the gh CLI's login; it needs permission to merge PRs and
delete branches (contents: write, pull_requests: write).`,
	Example: `  claims merge vm-dev7 --wait
  claims merge dev7 --dry-run                  # show the PR and its checks only
  claims merge delete-claim/dev7-vm --wait --yes
  claims merge 4442 --repo stuttgart-things/stuttgart-things
  claims merge https://github.com/stuttgart-things/stuttgart-things/pull/4442`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cfg := mergeFlags
		cfg.Arg = args[0]
		exitOn(runMerge(context.Background(), &cfg, os.Stdout))
	},
}

func init() {
	f := mergeCmd.Flags()
	f.StringVar(&mergeFlags.Repo, "repo", "", "Repository owner/name (default: the current checkout's GitHub repository; outside one $"+envFleetRepo+" or "+defaultFleetRepo+")")
	f.BoolVar(&mergeFlags.Wait, "wait", false, "Wait for the checks to finish, then merge (stops at the first failed check)")
	f.DurationVar(&mergeFlags.Timeout, "timeout", defaultMergeTimeout, "How long --wait waits for the checks")
	f.BoolVarP(&mergeFlags.Yes, "yes", "y", false, "Merge without asking")
	f.BoolVar(&mergeFlags.DryRun, "dry-run", false, "Show the PR and its checks, do not merge")
	rootCmd.AddCommand(mergeCmd)
}

const defaultMergeTimeout = 30 * time.Minute

var (
	// mergePoll is how often the checks and the PR are looked up.
	mergePoll = 10 * time.Second
	// mergeCheckGrace is how long --wait waits for a first check to show up
	// on a PR that has none yet (the workflows are still being queued).
	mergeCheckGrace = 2 * time.Minute
	// mergeStateTries is how often the PR's mergeable_state is read while
	// GitHub still computes it (unknown) or lags behind the checks (blocked).
	mergeStateTries = 6
)

// confirmMerge asks before the merge; a variable for tests.
var confirmMerge = func(title, consequence string) (bool, error) {
	ok := false
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(title).Description(consequence).
			Affirmative("Merge").Negative("Cancel").Value(&ok),
	)).Run()
	return ok, err
}

// stdinIsTerminal reports whether claims can ask; a variable for tests.
var stdinIsTerminal = func() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

// mergeCreatedPR merges the PR claims has just opened (--merge on render,
// change and delete): wait for the checks, then merge without asking -- the
// --merge flag is the confirmation. On failure the PR stays open.
func mergeCreatedPR(prURL, token string) error {
	fmt.Println()
	cfg := &MergeConfig{Arg: prURL, Wait: true, Yes: true, Timeout: defaultMergeTimeout, Token: token, created: true}
	if err := runMerge(context.Background(), cfg, os.Stdout); err != nil {
		return fmt.Errorf("%s was opened but not merged: %w", prURL, err)
	}
	return nil
}

var prURLPattern = regexp.MustCompile(`^https?://[^/]+/([^/]+/[^/]+)/pull/(\d+)(?:[/?#].*)?$`)

// prRef is what the argument names: a number (with the repository of a URL)
// or a branch/name.
type prRef struct {
	repo   string
	number int
	name   string
}

func parsePRRef(arg string) (prRef, error) {
	arg = strings.TrimSpace(arg)
	if m := prURLPattern.FindStringSubmatch(arg); m != nil {
		n, _ := strconv.Atoi(m[2])
		return prRef{repo: m[1], number: n}, nil
	}
	if strings.Contains(arg, "://") {
		return prRef{}, fmt.Errorf("%q is not a pull request URL (https://github.com/<owner>/<repo>/pull/<number>)", arg)
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(arg, "#")); err == nil {
		if n <= 0 {
			return prRef{}, fmt.Errorf("%q is not a PR number", arg)
		}
		return prRef{number: n}, nil
	}
	if arg == "" {
		return prRef{}, errors.New("name the PR: a name, a branch, a PR number or URL")
	}
	return prRef{name: arg}, nil
}

// recognisedBranch reports whether claims opens PRs on branch.
func recognisedBranch(branch string) bool {
	for _, p := range mergeBranchPrefixes {
		if strings.HasPrefix(branch, p) && len(branch) > len(p) {
			return true
		}
	}
	return false
}

// mergeBranchCandidates are the branches a name may be on. A VM is ordered as
// vm-<name> and its XR is <name>-vm, which change and delete use.
func mergeBranchCandidates(name string) []string {
	if recognisedBranch(name) {
		return []string{name}
	}
	out := []string{"vm-" + name}
	if base, ok := strings.CutSuffix(name, "-vm"); ok && base != "" {
		out = append(out, "vm-"+base)
	}
	out = append(out, "change-claim/"+name, "delete-claim/"+name)
	if !strings.HasSuffix(name, "-vm") {
		out = append(out, "change-claim/"+name+"-vm", "delete-claim/"+name+"-vm")
	}
	return out
}

// mergeConsequence is what merging the PR does, for the prompt: the title's
// MERGE = BAU for an order, the deletion for delete-claim.
func mergeConsequence(pr *github.PullRequest) string {
	branch, title := pr.GetHead().GetRef(), pr.GetTitle()
	switch {
	case strings.HasPrefix(branch, "delete-claim/"):
		what := strings.TrimPrefix(title, "Delete claim: ")
		if what == title {
			what = strings.TrimPrefix(branch, "delete-claim/")
		}
		return "DELETE: merging deletes " + what + " on the cluster (delete-claim-xr.yaml, after Flux removed it from the fleet)."
	case strings.HasPrefix(branch, "change-claim/"):
		return "CHANGE: merging applies the change to " + strings.TrimPrefix(branch, "change-claim/") +
			" (a resize reboots in its window; a new runID deletes the previous PipelineRun and its logs)."
	case strings.Contains(title, "MERGE = BAU"), strings.HasPrefix(branch, "vm-"):
		return "MERGE = BAU: merging builds " + strings.TrimPrefix(branch, "vm-") + " (Flux applies the order, Crossplane creates the VM)."
	default:
		return "Merging applies the change to the fleet (Flux)."
	}
}

func runMerge(ctx context.Context, cfg *MergeConfig, out io.Writer) error {
	ref, err := parsePRRef(cfg.Arg)
	if err != nil {
		return err
	}
	if ref.repo != "" {
		if cfg.Repo != "" && cfg.Repo != ref.repo {
			return fmt.Errorf("the URL names %s, --repo %s", ref.repo, cfg.Repo)
		}
		cfg.Repo = ref.repo
	}
	if cfg.Repo == "" {
		cc := &ChangeConfig{}
		if err := resolveChangeRepo(cc); err != nil {
			return err
		}
		cfg.Repo = cc.Repo
	}
	owner, repo, err := ghclient.SplitRepo(cfg.Repo)
	if err != nil {
		return err
	}
	c, err := ghclient.FromEnv(cfg.Token)
	if err != nil {
		return err
	}
	api := &changeGitHub{c: c, owner: owner, repo: repo}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultMergeTimeout
	}

	pr, err := findMergePR(ctx, api, ref)
	if err != nil {
		return err
	}
	if err := checkMergeable(pr, api, cfg.created); err != nil {
		return err
	}
	required, protection := requiredChecks(ctx, api, pr.GetBase().GetRef())
	files, _, err := api.c.PullRequests.ListFiles(ctx, owner, repo, pr.GetNumber(), &github.ListOptions{PerPage: 100})
	if err != nil {
		return ghclient.Describe(err, fmt.Sprintf("listing the files of PR #%d in %s", pr.GetNumber(), api))
	}
	checks, err := listChecks(ctx, api, pr.GetHead().GetSHA())
	if err != nil {
		return err
	}
	printMergePR(out, pr, files, required, checks)
	consequence := mergeConsequence(pr)
	_, _ = fmt.Fprintln(out, warnStyle.Render(consequence))

	if cfg.DryRun {
		_, _ = fmt.Fprintln(out, "\n=== DRY RUN - not merged ===")
		return nil
	}
	if !cfg.Yes {
		if !stdinIsTerminal() {
			return errors.New("not a terminal: pass --yes to merge without asking")
		}
		ok, err := confirmMerge(fmt.Sprintf("Merge PR #%d %q?", pr.GetNumber(), pr.GetTitle()), consequence)
		if err != nil {
			return err
		}
		if !ok {
			_, _ = fmt.Fprintln(out, "Cancelled, the PR stays open.")
			return nil
		}
	}

	if pr, err = waitChecks(ctx, api, pr, required, cfg, out); err != nil {
		return err
	}
	if pr, err = waitMergeable(ctx, api, pr, protection, out); err != nil {
		return err
	}
	return mergeAndDeleteBranch(ctx, api, pr, out)
}

// findMergePR reads the PR by number, or looks up the open PR of a name.
func findMergePR(ctx context.Context, api *changeGitHub, ref prRef) (*github.PullRequest, error) {
	if ref.number > 0 {
		pr, _, err := api.c.PullRequests.Get(ctx, api.owner, api.repo, ref.number)
		if err != nil {
			return nil, ghclient.Describe(err, fmt.Sprintf("reading PR #%d in %s", ref.number, api))
		}
		return pr, nil
	}
	var found []*github.PullRequest
	candidates := mergeBranchCandidates(ref.name)
	for _, b := range candidates {
		prs, _, err := api.c.PullRequests.List(ctx, api.owner, api.repo, &github.PullRequestListOptions{
			Head: api.owner + ":" + b, State: "open"})
		if err != nil {
			return nil, ghclient.Describe(err, "looking up the PR "+b+" in "+api.String())
		}
		found = append(found, prs...)
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no open PR on %s in %s", strings.Join(candidates, ", "), api)
	case 1:
		// List returns a PR without mergeable_state: read it.
		pr, _, err := api.c.PullRequests.Get(ctx, api.owner, api.repo, found[0].GetNumber())
		if err != nil {
			return nil, ghclient.Describe(err, fmt.Sprintf("reading PR #%d in %s", found[0].GetNumber(), api))
		}
		return pr, nil
	}
	var lines []string
	for _, p := range found {
		lines = append(lines, fmt.Sprintf("  #%d %s (%s)", p.GetNumber(), p.GetTitle(), p.GetHead().GetRef()))
	}
	return nil, fmt.Errorf("%q matches %d open PRs; name one by number or branch:\n%s", ref.name, len(found), strings.Join(lines, "\n"))
}

// checkMergeable is the guard rail before anything else: an open PR claims
// recognises, from the repository itself into its default branch.
func checkMergeable(pr *github.PullRequest, api *changeGitHub, created bool) error {
	n := pr.GetNumber()
	switch {
	case pr.GetMerged():
		return fmt.Errorf("PR #%d is merged already: %s", n, pr.GetHTMLURL())
	case pr.GetState() != "open":
		return fmt.Errorf("PR #%d is %s: %s", n, pr.GetState(), pr.GetHTMLURL())
	case pr.GetDraft():
		return fmt.Errorf("PR #%d is a draft: mark it ready for review first", n)
	}
	head, base := pr.GetHead(), pr.GetBase()
	if !strings.EqualFold(head.GetRepo().GetFullName(), api.String()) {
		return fmt.Errorf("PR #%d comes from %s, not from %s: claims merges only its own branches", n, head.GetRepo().GetFullName(), api)
	}
	if def := base.GetRepo().GetDefaultBranch(); def != "" && base.GetRef() != def {
		return fmt.Errorf("PR #%d goes into %s, not into the default branch %s", n, base.GetRef(), def)
	}
	if !created && !recognisedBranch(head.GetRef()) {
		return fmt.Errorf("PR #%d (branch %s) is not a claims PR: claims merges only %s branches",
			n, head.GetRef(), strings.Join(mergeBranchPrefixes, ", "))
	}
	return nil
}

// branchProtection is what claims could read about the base branch's
// required reviews (for the message when GitHub blocks the merge).
type branchProtection struct {
	approvals int // required approving reviews; 0 = none or unknown
}

// requiredChecks are the base branch's required status checks: classic
// branch protection plus rulesets. Neither readable (a plan without them, a
// token without the permission) counts as none.
func requiredChecks(ctx context.Context, api *changeGitHub, base string) ([]string, branchProtection) {
	var names []string
	var prot branchProtection
	add := func(n string) {
		if n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	if b, _, err := api.c.Repositories.GetBranch(ctx, api.owner, api.repo, base, 1); err == nil && b.GetProtection() != nil {
		p := b.GetProtection()
		if rsc := p.RequiredStatusChecks; rsc != nil {
			if rsc.Contexts != nil {
				for _, c := range *rsc.Contexts {
					add(c)
				}
			}
			if rsc.Checks != nil {
				for _, c := range *rsc.Checks {
					add(c.Context)
				}
			}
		}
		if r := p.RequiredPullRequestReviews; r != nil {
			prot.approvals = r.RequiredApprovingReviewCount
		}
	}
	if rules, _, err := api.c.Repositories.ListRulesForBranch(ctx, api.owner, api.repo, base, nil); err == nil && rules != nil {
		for _, r := range rules.RequiredStatusChecks {
			for _, c := range r.Parameters.RequiredStatusChecks {
				add(c.Context)
			}
		}
		for _, r := range rules.PullRequest {
			if r.Parameters.RequiredApprovingReviewCount > prot.approvals {
				prot.approvals = r.Parameters.RequiredApprovingReviewCount
			}
		}
	}
	return names, prot
}

// prCheck is one check run or commit status on the PR's head.
type prCheck struct {
	name  string
	state string // pending, success, failure
	url   string
}

// listChecks reads the check runs (latest per name) and commit statuses of sha.
func listChecks(ctx context.Context, api *changeGitHub, sha string) ([]prCheck, error) {
	var out []prCheck
	opts := &github.ListCheckRunsOptions{Filter: new("latest"), ListOptions: github.ListOptions{PerPage: 100}}
	for {
		res, resp, err := api.c.Checks.ListCheckRunsForRef(ctx, api.owner, api.repo, sha, opts)
		if err != nil {
			return nil, ghclient.Describe(err, "reading the checks of "+shortSHA(sha)+" in "+api.String())
		}
		for _, r := range res.CheckRuns {
			out = append(out, prCheck{name: r.GetName(), state: checkRunState(r), url: r.GetHTMLURL()})
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	st, _, err := api.c.Repositories.GetCombinedStatus(ctx, api.owner, api.repo, sha, &github.ListOptions{PerPage: 100})
	if err != nil {
		return nil, ghclient.Describe(err, "reading the statuses of "+shortSHA(sha)+" in "+api.String())
	}
	for _, s := range st.Statuses {
		state := "pending"
		switch s.GetState() {
		case "success":
			state = "success"
		case "failure", "error":
			state = "failure"
		}
		out = append(out, prCheck{name: s.GetContext(), state: state, url: s.GetTargetURL()})
	}
	return out, nil
}

func checkRunState(r *github.CheckRun) string {
	if r.GetStatus() != "completed" {
		return "pending"
	}
	switch r.GetConclusion() {
	case "success", "neutral", "skipped":
		return "success"
	}
	return "failure" // failure, cancelled, timed_out, action_required, stale, startup_failure
}

// evalChecks splits the checks that count -- the required ones, or all when
// none are required -- into pending and failed. A required check that has not
// reported yet is pending.
func evalChecks(checks []prCheck, required []string) (pending, failed []prCheck) {
	if len(required) == 0 {
		for _, c := range checks {
			switch c.state {
			case "pending":
				pending = append(pending, c)
			case "failure":
				failed = append(failed, c)
			}
		}
		return pending, failed
	}
	for _, name := range required {
		var seen []prCheck
		for _, c := range checks {
			if c.name == name {
				seen = append(seen, c)
			}
		}
		if len(seen) == 0 {
			pending = append(pending, prCheck{name: name, state: "expected"})
			continue
		}
		p, f := evalChecks(seen, nil)
		pending, failed = append(pending, p...), append(failed, f...)
	}
	return pending, failed
}

// waitChecks returns once the checks that count are green: without --wait
// right away (or with an error), with --wait after they finished, at the first
// failure, or at the timeout. It re-reads the PR, which may have moved on.
func waitChecks(ctx context.Context, api *changeGitHub, pr *github.PullRequest, required []string, cfg *MergeConfig, out io.Writer) (*github.PullRequest, error) {
	start := time.Now()
	deadline := start.Add(cfg.Timeout)
	settled, lastLine := false, ""
	for {
		checks, err := listChecks(ctx, api, pr.GetHead().GetSHA())
		if err != nil {
			return nil, err
		}
		pending, failed := evalChecks(checks, required)
		if len(failed) > 0 {
			return nil, fmt.Errorf("%s failed; the PR stays open: %s\n%s", plural(len(failed), "check"), pr.GetHTMLURL(), checkLines(failed))
		}
		switch {
		case !cfg.Wait && len(pending) > 0:
			return nil, fmt.Errorf("%s still running: wait for them (--wait); the PR stays open: %s\n%s",
				plural(len(pending), "check"), pr.GetHTMLURL(), checkLines(pending))
		case !cfg.Wait:
			return pr, nil
		case len(pending) == 0 && len(checks) == 0 && len(required) == 0 && time.Since(start) < mergeCheckGrace:
			// no check reported yet: the workflows may still be queued
		case len(pending) == 0 && settled:
			_, _ = fmt.Fprintln(out, successStyle.Render(fmt.Sprintf("Checks green (%d).", len(checks))))
			return pr, nil
		case len(pending) == 0:
			settled = true // once more: a workflow may queue another check
		default:
			settled = false
		}
		if len(pending) > 0 {
			if line := fmt.Sprintf("Waiting for %s: %s", plural(len(pending), "check"), checkNames(pending)); line != lastLine {
				_, _ = fmt.Fprintln(out, line)
				lastLine = line
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the checks did not finish within %s; the PR stays open: %s\n%s", cfg.Timeout, pr.GetHTMLURL(), checkLines(pending))
		}
		time.Sleep(mergePoll)
		sha := pr.GetHead().GetSHA()
		if pr, err = getPR(ctx, api, pr.GetNumber()); err != nil {
			return nil, err
		}
		if err := checkMergeable(pr, api, true); err != nil {
			return nil, err
		}
		if pr.GetHead().GetSHA() != sha {
			_, _ = fmt.Fprintf(out, "The branch moved on to %s: waiting for its checks.\n", shortSHA(pr.GetHead().GetSHA()))
			settled = false
		}
	}
}

// waitMergeable reads mergeable_state until GitHub has computed it, and
// accepts only the states that merge without a bypass.
func waitMergeable(ctx context.Context, api *changeGitHub, pr *github.PullRequest, prot branchProtection, out io.Writer) (*github.PullRequest, error) {
	var err error
	for i := 0; ; i++ {
		state := pr.GetMergeableState()
		switch state {
		case "clean", "has_hooks":
			return pr, nil
		case "unstable":
			_, _ = fmt.Fprintln(out, warnStyle.Render("GitHub reports checks that are not required as not green (mergeable_state unstable)."))
			return pr, nil
		case "dirty":
			return nil, fmt.Errorf("PR #%d has merge conflicts with %s; the PR stays open: %s", pr.GetNumber(), pr.GetBase().GetRef(), pr.GetHTMLURL())
		case "behind":
			return nil, fmt.Errorf("PR #%d is behind %s and the branch must be up to date: update the branch, then merge again; the PR stays open: %s",
				pr.GetNumber(), pr.GetBase().GetRef(), pr.GetHTMLURL())
		case "draft":
			return nil, fmt.Errorf("PR #%d is a draft; the PR stays open: %s", pr.GetNumber(), pr.GetHTMLURL())
		}
		// "", unknown (still computing) and blocked (may lag behind the checks)
		if i+1 >= mergeStateTries {
			if state == "blocked" {
				return nil, blockedError(ctx, api, pr, prot)
			}
			return nil, fmt.Errorf("GitHub did not report whether PR #%d can be merged (mergeable_state %q); the PR stays open: %s", pr.GetNumber(), state, pr.GetHTMLURL())
		}
		time.Sleep(mergePoll)
		if pr, err = getPR(ctx, api, pr.GetNumber()); err != nil {
			return nil, err
		}
	}
}

// blockedError says why GitHub blocks the merge, as far as claims can see.
func blockedError(ctx context.Context, api *changeGitHub, pr *github.PullRequest, prot branchProtection) error {
	reason := "branch protection blocks it (a required review or check is missing)"
	if reviews, _, err := api.c.PullRequests.ListReviews(ctx, api.owner, api.repo, pr.GetNumber(), &github.ListOptions{PerPage: 100}); err == nil {
		approved := map[string]bool{}
		for _, r := range reviews {
			switch r.GetState() {
			case "APPROVED":
				approved[r.GetUser().GetLogin()] = true
			case "CHANGES_REQUESTED", "DISMISSED":
				delete(approved, r.GetUser().GetLogin())
			}
		}
		if prot.approvals > 0 {
			reason = fmt.Sprintf("a required review is missing (%d of %d approvals) or a required check is not green", len(approved), prot.approvals)
		} else {
			reason = fmt.Sprintf("branch protection blocks it: a required review (%d approvals so far) or check is missing", len(approved))
		}
	}
	return fmt.Errorf("GitHub blocks merging PR #%d: %s. claims does not bypass branch protection; the PR stays open: %s",
		pr.GetNumber(), reason, pr.GetHTMLURL())
}

// mergeAndDeleteBranch squash-merges the PR at the head claims checked, then
// deletes the branch (unless the repository did that already).
func mergeAndDeleteBranch(ctx context.Context, api *changeGitHub, pr *github.PullRequest, out io.Writer) error {
	n := pr.GetNumber()
	res, _, err := api.c.PullRequests.Merge(ctx, api.owner, api.repo, n, "", &github.PullRequestOptions{
		MergeMethod: "squash",
		SHA:         pr.GetHead().GetSHA(),
		CommitTitle: fmt.Sprintf("%s (#%d)", pr.GetTitle(), n),
	})
	if err != nil {
		return mergeRefused(err, pr)
	}
	_, _ = fmt.Fprintln(out, successStyle.Render(fmt.Sprintf("Merged PR #%d (squash, %s): %s", n, shortSHA(res.GetSHA()), pr.GetHTMLURL())))

	branch := pr.GetHead().GetRef()
	if _, err := api.c.Git.DeleteRef(ctx, api.owner, api.repo, "heads/"+branch); err != nil {
		var gerr *github.ErrorResponse
		if errors.As(err, &gerr) && gerr.Response != nil && gerr.Response.StatusCode == http.StatusUnprocessableEntity {
			_, _ = fmt.Fprintf(out, "Branch %s is gone already.\n", branch)
			return nil
		}
		_, _ = fmt.Fprintln(out, warnStyle.Render(fmt.Sprintf("Merged, but the branch %s was not deleted: %v", branch, ghclient.Describe(err, "deleting "+branch))))
		return nil
	}
	_, _ = fmt.Fprintf(out, "Deleted branch %s.\n", branch)
	return nil
}

// mergeRefused is GitHub's reason for not merging, with the PR left open.
func mergeRefused(err error, pr *github.PullRequest) error {
	var gerr *github.ErrorResponse
	if errors.As(err, &gerr) && gerr.Response != nil {
		switch gerr.Response.StatusCode {
		case http.StatusMethodNotAllowed: // not mergeable: protection, reviews, checks
			return fmt.Errorf("GitHub refused the merge: %s; the PR stays open: %s", gerr.Message, pr.GetHTMLURL())
		case http.StatusConflict: // the head moved since claims read it
			return fmt.Errorf("GitHub refused the merge: %s (the branch moved on since the checks were read; run claims merge again); the PR stays open: %s", gerr.Message, pr.GetHTMLURL())
		}
	}
	return fmt.Errorf("%w; the PR stays open: %s", ghclient.Describe(err, fmt.Sprintf("merging PR #%d", pr.GetNumber())), pr.GetHTMLURL())
}

func getPR(ctx context.Context, api *changeGitHub, n int) (*github.PullRequest, error) {
	pr, _, err := api.c.PullRequests.Get(ctx, api.owner, api.repo, n)
	if err != nil {
		return nil, ghclient.Describe(err, fmt.Sprintf("reading PR #%d in %s", n, api))
	}
	return pr, nil
}

func printMergePR(out io.Writer, pr *github.PullRequest, files []*github.CommitFile, required []string, checks []prCheck) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(out, format, a...) }
	p("PR:       #%d %s\n", pr.GetNumber(), pr.GetHTMLURL())
	p("Title:    %s\n", pr.GetTitle())
	p("Branch:   %s -> %s (head %s)\n", pr.GetHead().GetRef(), pr.GetBase().GetRef(), shortSHA(pr.GetHead().GetSHA()))
	p("Author:   %s\n", pr.GetUser().GetLogin())
	var labels []string
	for _, l := range pr.Labels {
		labels = append(labels, l.GetName())
	}
	p("Labels:   %s\n", orNone(labels))
	p("Files:    %d\n", len(files))
	for _, f := range files {
		p("  %-8s %s\n", f.GetStatus(), f.GetFilename())
	}
	if len(required) > 0 {
		p("Required: %s\n", strings.Join(required, ", "))
	} else {
		p("Required: no required checks on %s: every check on the PR counts\n", pr.GetBase().GetRef())
	}
	pending, failed := evalChecks(checks, required)
	p("Checks:   %d reported, %d pending, %d failed\n", len(checks), len(pending), len(failed))
	for _, c := range failed {
		p("  failed   %s\n", c.name)
	}
	for _, c := range pending {
		p("  %-8s %s\n", c.state, c.name)
	}
}

func checkLines(cs []prCheck) string {
	var lines []string
	for _, c := range cs {
		l := fmt.Sprintf("  %s: %s", c.name, c.state)
		if c.url != "" {
			l += " " + c.url
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

func checkNames(cs []prCheck) string {
	var names []string
	for _, c := range cs {
		names = append(names, c.name)
	}
	return strings.Join(names, ", ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
