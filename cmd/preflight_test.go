package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/stuttgart-things/claims/internal/ghclient"
	"github.com/stuttgart-things/claims/internal/gitops"
)

// noGitToken clears every source of a git token (gh is stubbed in TestMain).
func noGitToken(t *testing.T) {
	t.Helper()
	for _, k := range []string{"GIT_USER", "GIT_TOKEN", "GITHUB_USER", "GITHUB_TOKEN"} {
		t.Setenv(k, "")
	}
}

// stubProbe replaces the git ls-remote probe.
func stubProbe(t *testing.T, err error) *[]string {
	t.Helper()
	var urls []string
	prev := probeRemote
	probeRemote = func(_ context.Context, url, _, _ string) error {
		urls = append(urls, url)
		return err
	}
	t.Cleanup(func() { probeRemote = prev })
	return &urls
}

// checkoutWithRemote is a git checkout with one commit and origin at url.
func checkoutWithRemote(t *testing.T, url string) string {
	t.Helper()
	dir := t.TempDir()
	r, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, _ := r.Worktree()
	_, _ = wt.Add("README.md")
	if _, err := wt.Commit("init", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func failed(r *preflightReport, name string) *checkResult {
	for i, c := range r.Results {
		if !c.OK && c.Name == name {
			return &r.Results[i]
		}
	}
	return nil
}

// apiServer answers GET /api/v1/claim-templates with status and body.
func apiServer(t *testing.T, status int, body string, wantAuth string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantAuth != "" && r.Header.Get("Authorization") != "Bearer "+wantAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/v1/claim-templates" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Real failure 1 of claims#114: whatever answered on the URL (localhost)
// returned "404 page not found" only when templates were fetched.
func TestPreflightAPINotClaimMachinery(t *testing.T) {
	isolateEndpoints(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	r := &preflightReport{}
	checkAPI(context.Background(), r, apiTarget{URL: srv.URL}, srv.URL, nil)
	c := failed(r, "API reachable")
	if c == nil || !strings.Contains(c.Detail, "not a claim-machinery-api") || !strings.Contains(c.Fix, "--endpoint") {
		t.Errorf("results = %+v", r.Results)
	}
}

func TestPreflightAPIAuth(t *testing.T) {
	isolateEndpoints(t)
	srv := apiServer(t, 200, `{"items":[{"metadata":{"name":"vm"}}]}`, "secret")

	r := &preflightReport{}
	checkAPI(context.Background(), r, apiTarget{URL: srv.URL, Name: "labda", Endpoint: endpointWithLogin()}, srv.URL, nil)
	c := failed(r, "API auth")
	if c == nil || !strings.Contains(c.Fix, "claims login --endpoint labda") {
		t.Fatalf("no credentials: %+v", r.Results)
	}

	t.Setenv("CLAIM_API_TOKEN", "secret")
	auth, err := resolveAPIAuth()
	if err != nil {
		t.Fatal(err)
	}
	r = &preflightReport{}
	checkAPI(context.Background(), r, apiTarget{URL: srv.URL}, srv.URL, auth)
	if r.Problems() != 0 || !strings.Contains(r.Results[0].Detail, "1 templates") {
		t.Errorf("with token: %+v", r.Results)
	}
}

// Real failure 2 of claims#114: SSH origin, no usable credentials -> render,
// branch and commit ran, then "pushing: invalid auth method".
func TestPreflightSSHRemote(t *testing.T) {
	noGitToken(t)
	probed := stubProbe(t, nil)
	repo := checkoutWithRemote(t, "git@github.com:stuttgart-things/stuttgart-things.git")
	plan := gitPreflight{RepoRoot: repo, Remote: "origin", Push: true}

	r := &preflightReport{}
	checkGit(context.Background(), r, plan)
	c := failed(r, "git token")
	if c == nil || !strings.Contains(c.Detail, "is SSH") || !strings.Contains(c.Fix, "gh auth") {
		t.Fatalf("no token: %+v", r.Results)
	}

	// With a token the SSH remote is pushed to over HTTPS.
	t.Setenv("GITHUB_TOKEN", "tok")
	r = &preflightReport{}
	checkGit(context.Background(), r, plan)
	if r.Problems() != 0 {
		t.Fatalf("with token: %+v", r.Results)
	}
	if len(*probed) != 1 || (*probed)[0] != "git@github.com:stuttgart-things/stuttgart-things.git" {
		t.Errorf("probed %v", *probed)
	}
	var out bytes.Buffer
	r.Print(&out, true)
	if !strings.Contains(out.String(), "https://github.com/stuttgart-things/stuttgart-things.git") {
		t.Errorf("no HTTPS note:\n%s", out.String())
	}

	// A token the remote refuses is a problem too, before anything is written.
	stubProbe(t, errors.New("authentication required"))
	r = &preflightReport{}
	checkGit(context.Background(), r, plan)
	if c := failed(r, "git remote"); c == nil || !strings.Contains(c.Detail, "refused the token") {
		t.Errorf("refused: %+v", r.Results)
	}
}

// Real failure 3 of claims#114 (and 1 again): "git credentials required"
// came only after the work was done. The preflight lists every problem at
// once, with the fix, fast and without network.
func TestPreflightListsAllProblems(t *testing.T) {
	isolateEndpoints(t)
	noGitToken(t)
	stubProbe(t, nil)
	repo := checkoutWithRemote(t, "https://github.com/o/r.git")
	g, _ := gitops.New(repo, "", "")
	_ = g.CreateBranch("vm-claims-auth") // left over from an earlier run

	start := time.Now()
	target, err := resolveAPITarget("", false)
	r := &preflightReport{}
	checkAPITarget(r, target, err)
	cfg := &RenderConfig{
		OutputDir: repo,
		GitConfig: &GitConfig{Commit: true, Push: true, Remote: "origin", Branch: "vm-claims-auth", CreateBranch: true},
		PRConfig:  &PRConfig{Create: true, BaseBranch: "main"},
	}
	addRenderGitChecks(r, cfg)
	perr := r.Err()
	if time.Since(start) > time.Second {
		t.Errorf("preflight took %s", time.Since(start))
	}
	if perr == nil {
		t.Fatal("no error")
	}
	msg := perr.Error()
	for _, want := range []string{
		"3 problem(s), nothing was written",
		"API endpoint", "claims endpoint add",
		"git branch", "already exists", "branch -D vm-claims-auth",
		"git token", "GITHUB_TOKEN", "gh auth login",
		"--skip-preflight",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
}

func TestPreflightBranchMustExistWithoutCreate(t *testing.T) {
	noGitToken(t)
	repo := checkoutWithRemote(t, "https://github.com/o/r.git")
	r := &preflightReport{}
	checkGit(context.Background(), r, gitPreflight{RepoRoot: repo, Branch: "nope"})
	if c := failed(r, "git branch"); c == nil || !strings.Contains(c.Fix, "--git-create-branch") {
		t.Errorf("%+v", r.Results)
	}
}

func TestPreflightGitHubPush(t *testing.T) {
	noGitToken(t)
	t.Setenv("GITHUB_TOKEN", "tok")
	stubProbe(t, nil)
	push := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if push {
			_, _ = w.Write([]byte(`{"full_name":"o/r","permissions":{"pull":true,"push":true}}`))
		} else {
			_, _ = w.Write([]byte(`{"full_name":"o/r","permissions":{"pull":true,"push":false}}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv(ghclient.EnvAPIURL, srv.URL)

	repo := checkoutWithRemote(t, "git@github.com:o/r.git")
	plan := gitPreflight{RepoRoot: repo, Push: true, PR: true}
	r := &preflightReport{}
	checkGit(context.Background(), r, plan)
	if c := failed(r, "GitHub PR"); c == nil || !strings.Contains(c.Detail, "may not push") {
		t.Errorf("read-only token: %+v", r.Results)
	}
	push = true
	r = &preflightReport{}
	checkGit(context.Background(), r, plan)
	if r.Problems() != 0 {
		t.Errorf("push token: %+v", r.Results)
	}
}

// After a commit, a failed push leaves the work where it is and says how to
// finish or undo it (claims#114).
func TestPushFailedHint(t *testing.T) {
	repo := checkoutWithRemote(t, "git@github.com:o/r.git")
	g, _ := gitops.New(repo, "", "")
	err := pushFailed(errors.New("pushing: invalid auth method"), g, "origin", "vm-claims-auth", "main", "", false)
	for _, want := range []string{
		"invalid auth method",
		"git -C " + repo + " push -u origin vm-claims-auth",
		"gh pr create --head vm-claims-auth --base main",
		"git -C " + repo + " switch main && git -C " + repo + " branch -D vm-claims-auth",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("lacks %q:\n%s", want, err)
		}
	}
	if err := pushFailed(errors.New("x"), g, "origin", "main", "main", "", false); !strings.Contains(err.Error(), "reset --soft HEAD~1") {
		t.Errorf("same branch: %v", err)
	}
	if err := pushFailed(errors.New("x"), g, "origin", "b", "main", "", true); !strings.Contains(err.Error(), "temporary clone") {
		t.Errorf("clone: %v", err)
	}
}

func TestDoctor(t *testing.T) {
	isolateEndpoints(t)
	srv := apiServer(t, 200, `{"items":[]}`, "")
	prev := doctorFlags
	t.Cleanup(func() { doctorFlags = prev })
	doctorFlags.noGit = true

	doctorFlags.apiURL = srv.URL
	var out bytes.Buffer
	if err := runDoctor(context.Background(), &out); err != nil {
		t.Fatalf("doctor: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "All checks passed") || !strings.Contains(out.String(), "API reachable") {
		t.Errorf("out:\n%s", out.String())
	}

	doctorFlags.apiURL = ""
	out.Reset()
	if err := runDoctor(context.Background(), &out); err == nil || !strings.Contains(out.String(), "FAIL  API endpoint") {
		t.Errorf("no endpoint: %v\n%s", err, out.String())
	}
}

// --merge needs the same GitHub token and push access as --create-pr; a
// merge failure after the PR exists gets no `gh pr create` hint.
func TestPreflightMergeAndMergeStep(t *testing.T) {
	p := gitPlanFor(&GitConfig{Push: true}, &PRConfig{Merge: true}, t.TempDir())
	if !p.PR {
		t.Error("--merge does not check GitHub access")
	}
	err := prFailed(mergeStep(errors.New("checks failed")), "b", "main")
	if strings.Contains(err.Error(), "gh pr create") {
		t.Errorf("merge failure got the PR hint: %v", err)
	}
	if err := prFailed(errors.New("422"), "b", "main"); !strings.Contains(err.Error(), "gh pr create --head b --base main") {
		t.Errorf("PR failure: %v", err)
	}
	if mergeStep(nil) != nil {
		t.Error("mergeStep(nil)")
	}
}
