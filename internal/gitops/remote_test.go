package gitops_test

import (
	"context"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"

	"github.com/stuttgart-things/claims/internal/gitops"
)

func TestHTTPSURL(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:stuttgart-things/claims.git":          "https://github.com/stuttgart-things/claims.git",
		"ssh://git@github.com/stuttgart-things/claims.git":    "https://github.com/stuttgart-things/claims.git",
		"ssh://git@github.com:22/stuttgart-things/claims":     "https://github.com/stuttgart-things/claims",
		"https://github.com/stuttgart-things/claims.git":      "https://github.com/stuttgart-things/claims.git",
		"/tmp/some/bare.git":                                  "/tmp/some/bare.git",
		"file:///tmp/some/bare.git":                           "file:///tmp/some/bare.git",
		"git+ssh://git@gitlab.example.com/group/sub/repo.git": "https://gitlab.example.com/group/sub/repo.git",
	} {
		if got := gitops.HTTPSURL(in); got != want {
			t.Errorf("HTTPSURL(%q) = %q, want %q", in, got, want)
		}
	}
	if gitops.IsSSHURL("/home/u@x/repo:1") {
		t.Error("a local path is not SSH")
	}
}

// addRemote points origin of the repo at url.
func addRemote(t *testing.T, repoPath, url string) {
	t.Helper()
	r, err := git.PlainOpen(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
}

// Real failure of claims#114: an SSH origin with a token failed with
// "pushing: invalid auth method" after the commit. Now the push goes over
// HTTPS with the token; here that ends at a closed port, not at the auth
// method.
func TestPushSSHRemoteUsesHTTPS(t *testing.T) {
	repo := initTestRepo(t)
	addRemote(t, repo, "git@127.0.0.1:o/r.git")
	g, err := gitops.New(repo, "", "tok")
	if err != nil {
		t.Fatal(err)
	}
	branch, _ := g.GetCurrentBranch()
	err = g.Push("origin", branch)
	if err == nil {
		t.Fatal("push to a closed port succeeded")
	}
	if strings.Contains(err.Error(), "invalid auth method") {
		t.Errorf("SSH remote still pushed with BasicAuth over SSH: %v", err)
	}
}

// A local (path) remote is pushed to without auth, also when a token is set.
func TestPushLocalRemoteWithToken(t *testing.T) {
	bare := t.TempDir()
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatal(err)
	}
	repo := initTestRepo(t)
	addRemote(t, repo, bare)
	g, err := gitops.New(repo, "", "tok")
	if err != nil {
		t.Fatal(err)
	}
	branch, _ := g.GetCurrentBranch()
	if err := g.Push("origin", branch); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := gitops.ProbeRemote(context.Background(), bare, "", "tok"); err != nil {
		t.Errorf("probe: %v", err)
	}
	if !g.BranchExists(branch) || g.BranchExists("nope") {
		t.Error("BranchExists")
	}
}
