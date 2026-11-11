package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stuttgart-things/claims/internal/apiauth"
	"github.com/stuttgart-things/claims/internal/ghclient"
	"github.com/stuttgart-things/claims/internal/gitops"
	"github.com/stuttgart-things/claims/internal/templates"
)

// Preflight (claims#114): before anything is rendered, branched or committed,
// check what the command will need -- the API URL, a usable login, a git
// token the remote accepts, a GitHub token for the PR -- and list every
// missing item at once with what fixes it.

// skipPreflight is --skip-preflight.
var skipPreflight bool

// addPreflightFlag registers --skip-preflight on c.
func addPreflightFlag(c *cobra.Command) {
	c.Flags().BoolVar(&skipPreflight, "skip-preflight", false, "Do not check API, login and git/GitHub credentials before starting")
}

// probeRemote is gitops.ProbeRemote; a variable for tests.
var probeRemote = gitops.ProbeRemote

// preflightTimeout bounds each network probe.
var preflightTimeout = 5 * time.Second

// checkResult is one check: OK, a warning, or a problem with its fix.
type checkResult struct {
	Name   string
	OK     bool
	Warn   bool
	Detail string
	Fix    string
}

// preflightReport collects the checks of one command.
type preflightReport struct {
	Results []checkResult
}

func (r *preflightReport) ok(name, detail string) {
	r.Results = append(r.Results, checkResult{Name: name, OK: true, Detail: detail})
}

func (r *preflightReport) warn(name, detail string) {
	r.Results = append(r.Results, checkResult{Name: name, OK: true, Warn: true, Detail: detail})
}

func (r *preflightReport) fail(name, problem, fix string) {
	r.Results = append(r.Results, checkResult{Name: name, Detail: problem, Fix: fix})
}

// failErr records err; a *fixError brings its own fix.
func (r *preflightReport) failErr(name string, err error, fix string) {
	var fe *fixError
	if errors.As(err, &fe) {
		r.fail(name, fe.msg, fe.fix)
		return
	}
	r.fail(name, err.Error(), fix)
}

// Problems counts the failed checks.
func (r *preflightReport) Problems() int {
	n := 0
	for _, c := range r.Results {
		if !c.OK {
			n++
		}
	}
	return n
}

// Print writes the checks; all=false prints only problems and warnings.
func (r *preflightReport) Print(w io.Writer, all bool) {
	for _, c := range r.Results {
		mark := "ok  "
		switch {
		case !c.OK:
			mark = "FAIL"
		case c.Warn:
			mark = "warn"
		case !all:
			continue
		}
		_, _ = fmt.Fprintf(w, "  %s  %-13s %s\n", mark, c.Name, c.Detail)
		if c.Fix != "" {
			_, _ = fmt.Fprintf(w, "        %-13s fix: %s\n", "", c.Fix)
		}
	}
}

// Err is nil without problems, else one error listing all of them.
func (r *preflightReport) Err() error {
	n := r.Problems()
	if n == 0 {
		return nil
	}
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "preflight: %d problem(s), nothing was written:\n", n)
	failed := preflightReport{}
	for _, c := range r.Results {
		if !c.OK {
			failed.Results = append(failed.Results, c)
		}
	}
	failed.Print(&sb, false)
	sb.WriteString("(--skip-preflight runs anyway; `claims doctor` runs the checks alone)")
	return errors.New(sb.String())
}

// finishPreflight prints warnings and returns the report's error.
func finishPreflight(r *preflightReport) error {
	for _, c := range r.Results {
		if c.Warn {
			warnf(c.Name + ": " + c.Detail)
		}
	}
	return r.Err()
}

// --- API -------------------------------------------------------------------

// checkAPITarget records the endpoint resolution.
func checkAPITarget(r *preflightReport, t apiTarget, err error) bool {
	if err != nil {
		r.failErr("API endpoint", err, "")
		return false
	}
	r.ok("API endpoint", fmt.Sprintf("%s (from %s)", t, t.Source))
	return true
}

// checkAPI probes url (one URL) the way render talks to it: the auth claims
// would use (explicit, else the cached login), then GET
// /api/v1/claim-templates, which also tells a claim-machinery-api from
// whatever else answers on the URL (claims#114: localhost answered 404).
func checkAPI(ctx context.Context, r *preflightReport, t apiTarget, url string, explicit *apiauth.Authenticator) {
	auth := explicit
	if auth == nil {
		if store, err := apiauth.DefaultStore(); err == nil {
			a, err := apiauth.Resolve(apiauth.Config{}, url, store, warnf)
			if err != nil {
				r.fail("API auth", err.Error(), "claims login "+t.loginArgs())
				return
			}
			auth = a
		}
	}
	if auth != nil {
		if _, err := auth.BearerToken(); err != nil {
			r.fail("API auth", fmt.Sprintf("%s credentials for %s cannot be used: %v", auth.Mode(), url, err), loginFix(t))
			return
		}
	}

	ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(url, "/")+"/api/v1/claim-templates", nil)
	if err != nil {
		r.fail("API reachable", err.Error(), "check the URL (-a/--endpoint)")
		return
	}
	resp, err := auth.HTTPClient(preflightTimeout).Do(req)
	if err != nil {
		r.fail("API reachable", fmt.Sprintf("%s: %v", url, err), "check the URL and network/VPN, or pick another API: --endpoint <name> / -a <url> (`claims endpoint list`)")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		if auth == nil {
			r.fail("API auth", fmt.Sprintf("%s requires authentication (%d) and no credentials are set", url, resp.StatusCode), loginFix(t))
		} else {
			r.fail("API auth", fmt.Sprintf("%s refused the %s token (%d)", url, auth.Mode(), resp.StatusCode), loginFix(t))
		}
		return
	case resp.StatusCode != http.StatusOK:
		r.fail("API reachable", fmt.Sprintf("%s is not a claim-machinery-api: GET /api/v1/claim-templates returned %d", url, resp.StatusCode),
			"pick the right API: --endpoint <name> / -a <url> (`claims endpoint list`)")
		return
	}
	var list templates.ClaimTemplateList
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		r.fail("API reachable", fmt.Sprintf("%s answered, but not like a claim-machinery-api: %v", url, err),
			"pick the right API: --endpoint <name> / -a <url>")
		return
	}
	r.ok("API reachable", fmt.Sprintf("%s (%d templates)", url, len(list.Items)))
	if auth == nil {
		r.ok("API auth", "none needed")
	} else {
		r.ok("API auth", string(auth.Mode()))
	}
}

// loginFix is how to log in to t, including what the endpoint still lacks.
func loginFix(t apiTarget) string {
	fix := "claims login " + t.loginArgs()
	switch {
	case t.Name != "" && t.Endpoint.Issuer == "":
		fix += fmt.Sprintf(" --issuer <url> --client-id <id> (or store them: claims endpoint add %s %s --issuer <url> --client-id <id>)", t.Name, t.URL)
	case t.Name == "":
		fix += " --issuer <url> --client-id <id>"
	}
	return fix + "; or set " + apiauth.EnvToken
}

// --- git and GitHub --------------------------------------------------------

// gitPreflight is what a command will do with git.
type gitPreflight struct {
	RepoRoot     string // local checkout ("" with RepoURL)
	RepoURL      string // clone instead
	Remote       string
	Branch       string
	CreateBranch bool
	Push         bool
	PR           bool
	User, Token  string // --git-user / --git-token
}

const gitTokenFix = "pass --git-token, set GIT_TOKEN or GITHUB_TOKEN (e.g. export GITHUB_TOKEN=$(gh auth token)), or log in with `gh auth login`"

// checkGit checks the git side: a checkout, a remote, a token it accepts,
// the branch, and for a PR a GitHub token that may push to the repository.
func checkGit(ctx context.Context, r *preflightReport, p gitPreflight) {
	var g *gitops.GitOps
	if p.RepoURL == "" {
		if p.RepoRoot == "" {
			r.fail("git repo", "the output directory is not in a git checkout", "use -o inside a checkout of the fleet repository, or --git-repo-url")
			return
		}
		var err error
		if g, err = gitops.New(p.RepoRoot, "", ""); err != nil {
			r.fail("git repo", err.Error(), "")
			return
		}
		r.ok("git repo", p.RepoRoot)
		if p.Branch != "" {
			exists := g.BranchExists(p.Branch)
			switch {
			case p.CreateBranch && exists:
				r.fail("git branch", fmt.Sprintf("branch %s already exists (left over from an earlier run?)", p.Branch),
					fmt.Sprintf("git -C %s branch -D %s, or choose another --git-branch", p.RepoRoot, p.Branch))
			case !p.CreateBranch && !exists:
				r.fail("git branch", fmt.Sprintf("branch %s does not exist", p.Branch), "add --git-create-branch")
			default:
				r.ok("git branch", p.Branch)
			}
		}
	}
	if !p.Push && !p.PR {
		return
	}

	url := p.RepoURL
	if g != nil {
		remote := p.Remote
		if remote == "" {
			remote = "origin"
		}
		u, err := g.GetRemoteURL(remote)
		if err != nil {
			r.fail("git remote", fmt.Sprintf("remote %s: %v", remote, err), "pass --git-remote <name>, or git remote add")
			return
		}
		url = u
	}

	token, tokErr := ghclient.Token(p.Token)
	if tokErr != nil {
		problem := "no token to push with"
		if gitops.IsSSHURL(url) {
			problem += fmt.Sprintf(" (%s is SSH; claims pushes over HTTPS with a token, the SSH agent is not used)", url)
		}
		r.fail("git token", problem, gitTokenFix)
	} else {
		r.ok("git token", "found")
		if gitops.IsSSHURL(url) {
			r.ok("git remote", fmt.Sprintf("%s is SSH: claims pushes over HTTPS with the token (%s)", url, gitops.HTTPSURL(url)))
		}
		pctx, cancel := context.WithTimeout(ctx, preflightTimeout)
		err := probeRemote(pctx, url, p.User, token)
		cancel()
		if err != nil {
			r.fail("git remote", fmt.Sprintf("%s refused the token or is unreachable: %v", url, err),
				"check that the token may write to the repository (contents: write); `gh auth status`; or "+gitTokenFix)
		} else {
			r.ok("git remote", gitops.HTTPSURL(url)+" accepts the token")
		}
	}

	if !p.PR {
		return
	}
	ownerRepo, err := ghclient.RepoFromURL(url)
	if err != nil {
		r.fail("GitHub PR", err.Error(), "--create-pr needs a GitHub remote")
		return
	}
	if tokErr != nil {
		return // reported above; the same token opens the PR
	}
	checkGitHubPush(ctx, r, token, ownerRepo)
}

// checkGitHubPush asks the GitHub API whether token may push to ownerRepo
// (needed to open the PR; the git token check covers the push itself).
func checkGitHubPush(ctx context.Context, r *preflightReport, token, ownerRepo string) {
	owner, repo, _ := ghclient.SplitRepo(ownerRepo)
	client, err := ghclient.New(token)
	if err != nil {
		r.fail("GitHub PR", err.Error(), "")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	rep, _, err := client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		r.fail("GitHub PR", ghclient.Describe(err, "reading "+ownerRepo).Error(), gitTokenFix)
		return
	}
	if p := rep.GetPermissions(); p != nil && !p.GetPush() {
		r.fail("GitHub PR", "the token may not push to "+ownerRepo, "use a token with contents: write and pull-requests: write")
		return
	}
	r.ok("GitHub PR", ownerRepo)
}

// gitPlanFor turns a GitConfig/PRConfig into the preflight plan; outputDir
// locates the checkout when there is no RepoURL.
func gitPlanFor(gc *GitConfig, pr *PRConfig, outputDir string) gitPreflight {
	p := gitPreflight{
		Remote:       gc.Remote,
		Branch:       gc.Branch,
		CreateBranch: gc.CreateBranch,
		Push:         gc.Push,
		PR:           pr != nil && (pr.Create || pr.Merge), // --merge needs the same token and push access
		RepoURL:      gc.RepoURL,
		User:         gc.User,
		Token:        gc.Token,
	}
	if p.RepoURL == "" {
		if root, err := findRepoRoot(outputDir); err == nil {
			p.RepoRoot = root
		}
	}
	return p
}

// preflightGitOnly checks just the git side (interactive flows, where git is
// chosen in a form after the API was checked).
func preflightGitOnly(gc *GitConfig, pr *PRConfig, outputDir string) error {
	if skipPreflight || gc == nil {
		return nil
	}
	r := &preflightReport{}
	checkGit(context.Background(), r, gitPlanFor(gc, pr, outputDir))
	return finishPreflight(r)
}
