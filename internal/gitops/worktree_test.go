package gitops_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stuttgart-things/claims/internal/gitops"
)

// A `git worktree` checkout keeps its objects in the main repository: New
// must find them, or CreateBranch fails with "object not found".
func TestNewOnGitWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	main := t.TempDir()
	wt := filepath.Join(t.TempDir(), "wt")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(main, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(main, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(main, "add", "a.txt")
	run(main, "commit", "-q", "-m", "init")
	run(main, "worktree", "add", "-q", "--detach", wt)

	g, err := gitops.New(wt, "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := g.CreateBranch("vm-test"); err != nil {
		t.Fatalf("CreateBranch on a worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.AddFiles([]string{filepath.Join(wt, "b.txt")}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	if err := g.Commit("add b", "t", "t@example.com"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}
