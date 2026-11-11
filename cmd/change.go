package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/google/go-github/v93/github"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/stuttgart-things/claims/internal/ghclient"
	"github.com/stuttgart-things/claims/internal/gitops"
	"github.com/stuttgart-things/claims/internal/xrdelete"
)

// claims change (claims#94): resize a VM claim in an *-xrs directory, or
// (claims#104) re-run its AnsibleRun by setting a new spec.ansible.runID.
//
// A thin wrapper around the fleet repository's dispatch-change-claim.yaml,
// the workflow behind Backstage's request-claim-change: the rules (create-vm's
// size table, a disk never shrinks, which fields may change, the one-shot
// resize run) stay in hack/claim-change.py. claims resolves the claim, starts
// the workflow with the same inputs, waits for it and prints the pull request
// change-claim/<name>, which pr-claim-change.yaml then checks.
//
// A runID change rebuilds the AnsibleRun (crossplane-configurations#555):
// deleting it does not re-run it (#554), so a retry is a change to the order
// file like a resize. claims checks what it can see (AnsibleRun enabled, the
// id new) in the local checkout, or without one on GitHub at --ref
// (claims#107); the workflow checks it again on main.

const changeWorkflow = "dispatch-change-claim.yaml"

// changeVMKinds are the kinds claim-change.py resizes and re-runs; both got
// spec.ansible.runID in crossplane-configurations#555 (HarvesterVM follows).
var changeVMKinds = map[string]bool{"NativeProxmoxVM": true, "NativeVsphereVM": true}

// runIDPattern is the XRD's pattern for spec.ansible.runID.
var runIDPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// ChangeConfig holds the inputs of a claim change.
type ChangeConfig struct {
	Kind        string
	Name        string
	Namespace   string
	SourcePath  string
	Size        string
	Reboot      string // now, evening, at, none -- or a time, which means at
	RebootAt    string
	RequestedBy string
	Reason      string

	RunID string // new spec.ansible.runID (re-runs the AnsibleRun)
	Retry bool   // count spec.ansible.runID up: unset -> 2, a number -> +1

	// From the fleet repository, when claims can read the file: the current
	// runID, the one --retry leads to, and where the PipelineRun runs.
	// Display only; the workflow decides on main.
	runIDSeen    bool
	runIDCurrent string
	runIDNext    string
	pipelineNS   string
	rebootSet    bool // --reboot given explicitly

	fleet    *fleetSource // where the claim was read; nil until opened
	fleetErr error        // why it could not be opened

	Repo   string // owner/name of the fleet repository ("" = the checkout's)
	Ref    string // branch the workflow runs on
	DryRun bool
	NoWait bool
	Merge  bool // wait for the PR's checks, then squash-merge it (claims#113)
}

var changeFlags ChangeConfig
var changeInteractive bool

var changeCmd = &cobra.Command{
	Use:     "change",
	Aliases: []string{"resize"},
	GroupID: groupClaims,
	Short:   "Change a claim (VM size, Ansible re-run) via Git PR",
	Long: `Resize a VM claim (NativeVsphereVM, NativeProxmoxVM) in an *-xrs directory of
the fleet repository, or re-run its AnsibleRun.

claims starts the repository's dispatch-change-claim.yaml with the same inputs
as Backstage's request-claim-change, waits for it and prints the pull request
change-claim/<name>. What may change is decided there (hack/claim-change.py):
cpu, memory and disk from create-vm's size table, and the disk never shrinks.
pr-claim-change.yaml checks the PR; merging it applies the new size.

--reboot decides when the one-shot run applies the resize in the guest:
now, evening (19-22 Europe/Berlin, default), none, or a time (ISO-8601, UTC
or Europe/Berlin without offset).

--run-id / --retry re-run a failed AnsibleRun instead: the PR sets
spec.ansible.runID, which makes Crossplane render the AnsibleRun afresh from
the current spec and EnvironmentConfig defaults, with a new PipelineRun
<name>-ansible-<runID>. --retry counts up (unset -> 2, a number -> +1). The
claim's spec.ansible.enabled must be true. Merging DELETES the previous
PipelineRun and its logs: read them first. Needs vspherevm >= v0.14.0 /
proxmoxvm >= v0.20.0 on the management cluster.

The claim is --name, --source-path or the argument (a name, or a path ending
in .yaml).

Talks to the GitHub API (no gh CLI needed): the token is $GIT_TOKEN,
$GITHUB_TOKEN, else the gh CLI's login; it needs permission to run
workflows (actions: write) and to read the repository.`,
	Example: `  claims change --kind NativeVsphereVM --name dev7-vm --size xlarge --reboot evening
  claims change --source-path clusters/labda/vsphere/machinery-orders-xrs/dev7.yaml --size xlarge --reboot 2026-11-05T20:00
  claims change --name dev7-vm --size xlarge --dry-run
  claims change dev7-vm --retry --reason "collection fixed"
  claims change dev7-vm --run-id fix-docker --dry-run`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cfg := changeFlags
		cfg.rebootSet = cmd.Flags().Changed("reboot")
		if len(args) == 1 {
			if err := claimArg(&cfg, args[0]); err != nil {
				exitOn(err)
			}
		}
		if cfg.RequestedBy == "" {
			cfg.RequestedBy, _ = gitops.ResolveCredentialsOptional("", "")
		}
		interactive := changeInteractive ||
			(cfg.Name == "" && cfg.SourcePath == "" && (isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())))
		exitOn(runChange(&cfg, interactive))
	},
}

func init() {
	f := changeCmd.Flags()
	f.StringVar(&changeFlags.Kind, "kind", "", "XR kind (NativeVsphereVM, NativeProxmoxVM); optional when the name is unique")
	f.StringVar(&changeFlags.Name, "name", "", "XR name (metadata.name)")
	f.StringVar(&changeFlags.Namespace, "namespace", "default", "XR namespace")
	f.StringVar(&changeFlags.SourcePath, "source-path", "", "File the XR lives in (its stuttgart-things.com/source-path)")
	f.StringVar(&changeFlags.Size, "size", "", "New size (create-vm's: small, medium, large, xlarge)")
	f.StringVar(&changeFlags.Reboot, "reboot", "evening", "When to apply the resize in the guest: now, evening, none, or an ISO-8601 time")
	f.StringVar(&changeFlags.RunID, "run-id", "", "New spec.ansible.runID: re-run the VM's AnsibleRun (instead of --size)")
	f.BoolVar(&changeFlags.Retry, "retry", false, "Re-run the AnsibleRun with the next runID (unset -> 2, a number -> +1)")
	f.StringVar(&changeFlags.RequestedBy, "requested-by", "", "Who asks for the change (PR body; default: the git user)")
	f.StringVar(&changeFlags.Reason, "reason", "", "Why the claim changes (PR body)")
	f.StringVar(&changeFlags.Repo, "repo", "", "Fleet repository owner/name (default: the current checkout's GitHub repository; outside one $"+envFleetRepo+" or "+defaultFleetRepo+", read from GitHub)")
	f.StringVar(&changeFlags.Ref, "ref", "main", "Branch the workflow runs on")
	f.BoolVar(&changeFlags.DryRun, "dry-run", false, "Print the workflow inputs without starting it")
	f.BoolVar(&changeFlags.NoWait, "no-wait", false, "Start the workflow and return without waiting for the PR")
	f.BoolVar(&changeFlags.Merge, "merge", false, "Wait for the PR's checks, then squash-merge it (see claims merge)")
	f.BoolVarP(&changeInteractive, "interactive", "i", false, "Pick the VM and the change interactively")
	rootCmd.AddCommand(changeCmd)
}

// claimArg takes the positional claim: a path ending in .yaml, else a name.
func claimArg(cfg *ChangeConfig, arg string) error {
	if strings.HasSuffix(arg, ".yaml") {
		if cfg.SourcePath != "" && cfg.SourcePath != arg {
			return fmt.Errorf("the argument %q and --source-path %q name two claims", arg, cfg.SourcePath)
		}
		cfg.SourcePath = arg
		return nil
	}
	if cfg.Name != "" && cfg.Name != arg {
		return fmt.Errorf("the argument %q and --name %q name two claims", arg, cfg.Name)
	}
	cfg.Name = arg
	return nil
}

// isRunID reports whether the change is a runID change (an Ansible re-run).
func (c *ChangeConfig) isRunID() bool { return c.RunID != "" || c.Retry }

// changeGitHub is the API client and the fleet repository the workflow runs in.
type changeGitHub struct {
	c           *github.Client
	owner, repo string
}

func (g *changeGitHub) String() string { return g.owner + "/" + g.repo }

// resolveChangeRepo fills cfg.Repo: --repo, the repository claims read the
// claim from, the checkout's origin, else $CLAIMS_GITHUB_REPO or the default.
func resolveChangeRepo(cfg *ChangeConfig) error {
	if cfg.Repo == "" {
		if src, ok := localFleet("."); ok {
			g, err := gitops.New(src.Root, "", "")
			if err != nil {
				return err
			}
			url, err := g.GetRemoteURL("origin")
			if err != nil {
				return fmt.Errorf("the checkout %s has no origin remote to start the workflow in: pass --repo", src.Root)
			}
			if cfg.Repo, err = ghclient.RepoFromURL(url); err != nil {
				return fmt.Errorf("%w: pass --repo", err)
			}
		} else {
			cfg.Repo = fleetRepoName("")
		}
	}
	_, _, err := ghclient.SplitRepo(cfg.Repo)
	return err
}

func newChangeGitHub(cfg *ChangeConfig) (*changeGitHub, error) {
	owner, repo, err := ghclient.SplitRepo(cfg.Repo)
	if err != nil {
		return nil, err
	}
	c, err := ghclient.FromEnv("")
	if err != nil {
		return nil, err
	}
	return &changeGitHub{c: c, owner: owner, repo: repo}, nil
}

// changePoll is how often the workflow run and the PR are looked up.
var changePoll = 10 * time.Second

func runChange(cfg *ChangeConfig, interactive bool) error {
	if interactive {
		ok, err := pickChangeInteractive(cfg)
		if err != nil || !ok {
			return err
		}
	}
	if cfg.Merge && cfg.NoWait {
		return errors.New("--merge waits for the PR: drop --no-wait")
	}
	if err := resolveChange(cfg); err != nil {
		return err
	}
	if err := resolveChangeRepo(cfg); err != nil {
		return err
	}
	inputs := changeInputs(cfg)
	printChange(cfg, inputs)
	if cfg.DryRun {
		fmt.Println("\n=== DRY RUN - workflow not started ===")
		return nil
	}

	api, err := newChangeGitHub(cfg)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if cfg.RequestedBy == "" {
		if u, _, err := api.c.Users.Get(ctx, ""); err == nil {
			cfg.RequestedBy = u.GetLogin()
			inputs = changeInputs(cfg)
		}
	}

	started := time.Now().Add(-5 * time.Second)
	body := map[string]any{}
	for _, kv := range inputs {
		body[kv[0]] = kv[1]
	}
	details, _, err := api.c.Actions.CreateWorkflowDispatchEventByFileName(ctx, api.owner, api.repo, changeWorkflow,
		github.CreateWorkflowDispatchEventRequest{Ref: cfg.Ref, Inputs: body, ReturnRunDetails: new(true)})
	if err != nil {
		return ghclient.Describe(err, fmt.Sprintf("starting %s on %s in %s", changeWorkflow, cfg.Ref, api))
	}
	fmt.Printf("Started %s on %s.\n", changeWorkflow, cfg.Ref)
	if cfg.NoWait {
		if u := details.GetHTMLURL(); u != "" {
			fmt.Println("Run: " + u)
		}
		fmt.Printf("The PR will be change-claim/%s.\n", cfg.Name)
		return nil
	}

	runID := details.GetWorkflowRunID()
	if runID == 0 { // an API without return_run_details: look the run up
		if runID, err = findDispatchedRun(ctx, api, started); err != nil {
			return err
		}
	}
	fmt.Printf("Waiting for run %d ...\n", runID)
	run, err := waitRun(ctx, api, runID)
	if err != nil {
		return err
	}
	if c := run.GetConclusion(); c != "success" {
		return fmt.Errorf("%s run %d: %s\n%s", changeWorkflow, runID, c, failedRunLines(ctx, api, run))
	}
	url, err := changePR(ctx, api, cfg)
	if err != nil {
		return err
	}
	fmt.Println(successStyle.Render("Created PR: " + url))
	if cfg.isRunID() {
		fmt.Println("pr-claim-change.yaml checks it; merging it starts the new PipelineRun.")
		fmt.Println(warnStyle.Render(runIDWarning(cfg)))
	} else {
		fmt.Println("pr-claim-change.yaml checks it; merging it applies the new size.")
	}
	if cfg.Merge {
		return mergeCreatedPR(url, "")
	}
	return nil
}

// runIDWarning: merging deletes the previous PipelineRun and its logs.
func runIDWarning(cfg *ChangeConfig) string {
	prev := cfg.Name + "-ansible"
	if cfg.runIDCurrent != "" {
		prev += "-" + cfg.runIDCurrent
	}
	where := ""
	if cfg.pipelineNS != "" {
		where = " (namespace " + cfg.pipelineNS + ")"
	}
	if !cfg.runIDSeen {
		prev = "of " + cfg.Name + "-ansible*"
	}
	return fmt.Sprintf("Read the logs of the previous PipelineRun %s%s before merging: the merge deletes it and its logs.", prev, where)
}

// resolveChange fills kind/name/source-path and checks the inputs claims can
// check without the rules (those stay in hack/claim-change.py).
func resolveChange(cfg *ChangeConfig) error {
	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}
	switch {
	case cfg.RunID != "" && cfg.Retry:
		return errors.New("--run-id and --retry exclude each other")
	case cfg.Size != "" && cfg.isRunID():
		return errors.New("--size and --run-id/--retry are two changes: one pull request each")
	case cfg.Size == "" && !cfg.isRunID():
		return errors.New("--size, --run-id or --retry is required")
	case cfg.RunID != "" && !runIDPattern.MatchString(cfg.RunID):
		return fmt.Errorf("--run-id %q: want lower-case letters, digits and '-', starting and ending with a letter or digit", cfg.RunID)
	case cfg.isRunID() && cfg.rebootSet:
		return errors.New("--reboot belongs to a resize; a re-run does not reboot the VM")
	}
	if cfg.SourcePath == "" || cfg.Kind == "" || cfg.Name == "" {
		src, err := cfg.openFleet()
		if err != nil {
			return fmt.Errorf("%w\n(or pass --kind, --name and --source-path: the workflow then checks the claim alone)", err)
		}
		x := &DeleteXRConfig{Kind: cfg.Kind, Name: cfg.Name, Namespace: cfg.Namespace, SourcePath: cfg.SourcePath}
		if err := resolveXRFS(src.FS, x); err != nil {
			return fmt.Errorf("%w (read from %s)", err, src)
		}
		cfg.Kind, cfg.Name, cfg.Namespace, cfg.SourcePath = x.Kind, x.Name, x.Namespace, x.SourcePath
	}
	if err := xrdelete.CheckPath(cfg.SourcePath); err != nil {
		return err
	}
	if !changeVMKinds[cfg.Kind] {
		return fmt.Errorf("%s cannot be changed this way: only NativeVsphereVM and NativeProxmoxVM are resized or re-run", cfg.Kind)
	}
	if cfg.isRunID() {
		cfg.Reboot, cfg.RebootAt = "", ""
		return checkRunID(cfg)
	}
	switch cfg.Reboot {
	case "", "evening":
		cfg.Reboot = "evening"
	case "now", "none":
	case "at":
		if cfg.RebootAt == "" {
			return errors.New("--reboot at needs a time: pass it as --reboot <ISO-8601 time>")
		}
	default:
		if _, err := parseRebootTime(cfg.Reboot); err != nil {
			return fmt.Errorf("--reboot %q: want now, evening, none or an ISO-8601 time (%v)", cfg.Reboot, err)
		}
		cfg.RebootAt, cfg.Reboot = cfg.Reboot, "at"
	}
	return nil
}

// openFleet opens the fleet repository once: the local checkout, else GitHub
// at --ref. A remote read names its repository in cfg.Repo, so the workflow
// is started there too.
func (cfg *ChangeConfig) openFleet() (*fleetSource, error) {
	if cfg.fleet == nil && cfg.fleetErr == nil {
		cfg.fleet, cfg.fleetErr = openFleet(cfg.Repo, cfg.Ref)
		if cfg.fleet != nil && cfg.fleet.remote() && cfg.Repo == "" {
			cfg.Repo = cfg.fleet.Repo
		}
	}
	return cfg.fleet, cfg.fleetErr
}

// checkRunID reads the claim from the fleet repository, when claims can:
// the AnsibleRun must be enabled and the id new; --retry's next id is worked
// out for display. Without the file the workflow checks alone.
func checkRunID(cfg *ChangeConfig) error {
	src, err := cfg.openFleet()
	if err != nil {
		return nil
	}
	data, err := fs.ReadFile(src.FS, cfg.SourcePath)
	if err != nil {
		return nil
	}
	var doc struct {
		Spec struct {
			Ansible struct {
				Enabled           bool   `yaml:"enabled"`
				RunID             any    `yaml:"runID"`
				PipelineNamespace string `yaml:"pipelineNamespace"`
			} `yaml:"ansible"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil // more than one document or not YAML: the workflow refuses it
	}
	a := doc.Spec.Ansible
	behind := " (in this checkout; git pull if it is behind main)"
	if src.remote() {
		behind = " (on " + src.String() + ")"
	}
	if !a.Enabled {
		return fmt.Errorf("%s: spec.ansible.enabled is not true, so there is no AnsibleRun to re-run%s", cfg.SourcePath, behind)
	}
	cfg.runIDSeen, cfg.pipelineNS = true, a.PipelineNamespace
	if a.RunID != nil {
		cfg.runIDCurrent = fmt.Sprint(a.RunID)
	}
	if cfg.Retry {
		next, err := nextRunID(cfg.runIDCurrent)
		if err != nil {
			return fmt.Errorf("%s: %w", cfg.SourcePath, err)
		}
		cfg.runIDNext = next
		return nil
	}
	if cfg.RunID == cfg.runIDCurrent {
		return fmt.Errorf("%s: spec.ansible.runID is already %q; a re-run needs a new one%s", cfg.SourcePath, cfg.RunID, behind)
	}
	return nil
}

// nextRunID is --retry's rule (claim-change.py's next_run_id).
func nextRunID(current string) (string, error) {
	if current == "" {
		return "2", nil
	}
	n, err := strconv.Atoi(current)
	if err != nil || n < 0 || strconv.Itoa(n) != current {
		return "", fmt.Errorf("spec.ansible.runID is %q, not a number: pass the next one with --run-id", current)
	}
	return strconv.Itoa(n + 1), nil
}

func parseRebootTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("not an ISO-8601 time")
}

// changeInputs are the workflow_dispatch inputs, in the workflow's order.
func changeInputs(cfg *ChangeConfig) [][2]string {
	in := [][2]string{
		{"kind", cfg.Kind},
		{"name", cfg.Name},
		{"namespace", cfg.Namespace},
		{"source_path", cfg.SourcePath},
	}
	switch {
	case cfg.RunID != "":
		in = append(in, [2]string{"run_id", cfg.RunID})
	case cfg.Retry:
		in = append(in, [2]string{"retry", "true"})
	default:
		in = append(in, [2]string{"size", cfg.Size}, [2]string{"reboot", cfg.Reboot})
	}
	if cfg.RebootAt != "" {
		in = append(in, [2]string{"reboot_at", cfg.RebootAt})
	}
	if cfg.RequestedBy != "" {
		in = append(in, [2]string{"requested_by", cfg.RequestedBy})
	}
	if cfg.Reason != "" {
		in = append(in, [2]string{"reason", cfg.Reason})
	}
	return in
}

func printChange(cfg *ChangeConfig, inputs [][2]string) {
	fmt.Printf("Workflow: %s on %s, repository %s\n", changeWorkflow, cfg.Ref, cfg.Repo)
	for _, kv := range inputs {
		fmt.Printf("  %-13s %s\n", kv[0]+":", kv[1])
	}
	if cfg.isRunID() {
		fmt.Printf("runID:    %s\n", runIDLine(cfg))
	}
	fmt.Printf("PR:       change-claim/%s\n", cfg.Name)
	if cfg.isRunID() {
		fmt.Println(warnStyle.Render(runIDWarning(cfg)))
	}
}

// runIDLine is "<current> -> <new>, PipelineRun <name>-ansible-<new>".
func runIDLine(cfg *ChangeConfig) string {
	next := cfg.RunID
	if next == "" {
		next = cfg.runIDNext
	}
	if !cfg.runIDSeen {
		if next == "" {
			return "next (the workflow counts up; claims could not read the file)"
		}
		return next + " (claims could not read the file)"
	}
	cur := cfg.runIDCurrent
	if cur == "" {
		cur = "(unset)"
	}
	return fmt.Sprintf("%s -> %s, PipelineRun %s-ansible-%s", cur, next, cfg.Name, next)
}

// findDispatchedRun returns the first workflow_dispatch run of the workflow
// created after `after`.
func findDispatchedRun(ctx context.Context, api *changeGitHub, after time.Time) (int64, error) {
	for i := 0; i < 30; i++ {
		runs, _, err := api.c.Actions.ListWorkflowRunsByFileName(ctx, api.owner, api.repo, changeWorkflow,
			&github.ListWorkflowRunsOptions{Event: "workflow_dispatch", ListOptions: github.ListOptions{PerPage: 10}})
		if err != nil {
			return 0, ghclient.Describe(err, "listing the "+changeWorkflow+" runs in "+api.String())
		}
		var best *github.WorkflowRun
		for _, r := range runs.WorkflowRuns {
			if r.GetCreatedAt().After(after) && (best == nil || r.GetCreatedAt().Before(best.GetCreatedAt().Time)) {
				best = r
			}
		}
		if best != nil {
			return best.GetID(), nil
		}
		time.Sleep(changePoll)
	}
	return 0, fmt.Errorf("the %s run did not show up; look at the repository's Actions tab", changeWorkflow)
}

func waitRun(ctx context.Context, api *changeGitHub, id int64) (*github.WorkflowRun, error) {
	for i := 0; i < 180; i++ {
		r, _, err := api.c.Actions.GetWorkflowRunByID(ctx, api.owner, api.repo, id)
		if err != nil {
			return nil, ghclient.Describe(err, fmt.Sprintf("reading run %d in %s", id, api))
		}
		if r.GetStatus() == "completed" {
			return r, nil
		}
		time.Sleep(changePoll)
	}
	return nil, fmt.Errorf("run %d did not finish in time", id)
}

func changePR(ctx context.Context, api *changeGitHub, cfg *ChangeConfig) (string, error) {
	for i := 0; i < 6; i++ {
		prs, _, err := api.c.PullRequests.List(ctx, api.owner, api.repo, &github.PullRequestListOptions{
			Head: api.owner + ":change-claim/" + cfg.Name, State: "open"})
		if err != nil {
			return "", ghclient.Describe(err, "looking up the PR change-claim/"+cfg.Name+" in "+api.String())
		}
		if len(prs) > 0 {
			return prs[0].GetHTMLURL(), nil
		}
		time.Sleep(changePoll)
	}
	return "", fmt.Errorf("the run succeeded but no open PR change-claim/%s was found", cfg.Name)
}

// failedRunLines are claim-change.py's refusals: its ::error:: lines, which
// GitHub keeps as annotations of the failed jobs.
func failedRunLines(ctx context.Context, api *changeGitHub, run *github.WorkflowRun) string {
	var msgs []string
	jobs, _, err := api.c.Actions.ListWorkflowJobs(ctx, api.owner, api.repo, run.GetID(), &github.ListWorkflowJobsOptions{Filter: "latest"})
	if err == nil {
		for _, j := range jobs.Jobs {
			if j.GetConclusion() != "failure" {
				continue
			}
			anns, _, err := api.c.Checks.ListCheckRunAnnotations(ctx, api.owner, api.repo, j.GetID(), nil)
			if err != nil {
				continue
			}
			for _, a := range anns {
				if a.GetAnnotationLevel() == "failure" { // not the runner's notices and warnings
					msgs = append(msgs, a.GetMessage())
				}
			}
		}
	}
	return refusalLines(msgs, run.GetHTMLURL())
}

// refusalLines keeps the messages that explain the failure: the runner's own
// "Process completed with exit code" note says nothing.
func refusalLines(msgs []string, runURL string) string {
	var out []string
	for _, m := range msgs {
		if m = strings.TrimSpace(m); m != "" && !strings.HasPrefix(m, "Process completed with exit code") {
			out = append(out, "  "+m)
		}
	}
	if len(out) == 0 {
		if runURL == "" {
			return "  (see the run's log)"
		}
		return "  (see the run's log: " + runURL + ")"
	}
	return strings.Join(out, "\n")
}

// pickChangeInteractive asks for the VM and the change: a size and the
// reboot window, or an Ansible re-run.
func pickChangeInteractive(cfg *ChangeConfig) (bool, error) {
	if cfg.Name == "" && cfg.SourcePath == "" {
		src, err := cfg.openFleet()
		if err != nil {
			return false, err
		}
		all, err := xrdelete.ScanFS(src.FS)
		if err != nil {
			return false, err
		}
		var options []huh.Option[string]
		for _, c := range all {
			if changeVMKinds[c.Kind] {
				options = append(options, huh.NewOption(fmt.Sprintf("%-16s %s/%s  (%s)", c.Kind, c.Namespace, c.Name, c.File), c.File))
			}
		}
		if len(options) == 0 {
			fmt.Println("No VM claims in the *-xrs directories.")
			return false, nil
		}
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Select the VM").Options(options...).Height(15).Value(&cfg.SourcePath),
		)).Run(); err != nil {
			return false, err
		}
	}
	action := "size"
	if cfg.isRunID() {
		action = "run-id"
	} else if cfg.Size == "" {
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("What to change").Options(
				huh.NewOption("Size (cpu, memory, disk)", "size"),
				huh.NewOption("Re-run Ansible (new spec.ansible.runID)", "run-id"),
			).Value(&action),
		)).Run(); err != nil {
			return false, err
		}
	}
	if action == "run-id" {
		return pickRunIDInteractive(cfg)
	}
	if cfg.Size == "" {
		cfg.Size = "large"
	}
	var confirm bool
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("New size").Options(
			huh.NewOption("small", "small"), huh.NewOption("medium", "medium"),
			huh.NewOption("large", "large"), huh.NewOption("xlarge", "xlarge"),
		).Value(&cfg.Size),
		huh.NewSelect[string]().Title("Apply in the guest").Options(
			huh.NewOption("evening (19-22 Europe/Berlin)", "evening"),
			huh.NewOption("now, right after the merge", "now"),
			huh.NewOption("none (reboot and grow by hand)", "none"),
		).Value(&cfg.Reboot),
		huh.NewInput().Title("Reason (optional)").Value(&cfg.Reason),
		huh.NewConfirm().Title("Open the change-claim PR?").Affirmative("Yes").Negative("Cancel").Value(&confirm),
	)).Run(); err != nil {
		return false, err
	}
	if !confirm {
		fmt.Println("Cancelled.")
	}
	return confirm, nil
}

// pickRunIDInteractive asks for the runID (empty: count up) and the reason.
func pickRunIDInteractive(cfg *ChangeConfig) (bool, error) {
	var confirm bool
	if err := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("New runID").
			Description("Empty: count up (unset -> 2, a number -> +1)").
			Validate(func(v string) error {
				if v != "" && !runIDPattern.MatchString(v) {
					return errors.New("lower-case letters, digits and '-'")
				}
				return nil
			}).Value(&cfg.RunID),
		huh.NewInput().Title("Reason (optional)").Value(&cfg.Reason),
		huh.NewNote().Title("Merging deletes the previous PipelineRun and its logs").
			Description("Read them before you merge the PR."),
		huh.NewConfirm().Title("Open the change-claim PR?").Affirmative("Yes").Negative("Cancel").Value(&confirm),
	)).Run(); err != nil {
		return false, err
	}
	cfg.Retry = cfg.RunID == ""
	if !confirm {
		fmt.Println("Cancelled.")
	}
	return confirm, nil
}
