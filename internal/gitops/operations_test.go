package gitops_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stuttgart-things/claims/internal/gitops"
)

func TestNew(t *testing.T) {
	// Create a test repo
	repoPath := initTestRepo(t)

	tests := []struct {
		name    string
		path    string
		user    string
		token   string
		wantErr bool
	}{
		{
			name:    "valid repo without auth",
			path:    repoPath,
			user:    "",
			token:   "",
			wantErr: false,
		},
		{
			name:    "valid repo with auth",
			path:    repoPath,
			user:    "testuser",
			token:   "testtoken",
			wantErr: false,
		},
		{
			name:    "invalid repo path",
			path:    "/nonexistent/path",
			user:    "",
			token:   "",
			wantErr: true,
		},
		{
			name:    "not a git repo",
			path:    t.TempDir(),
			user:    "",
			token:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := gitops.New(tt.path, tt.user, tt.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && g == nil {
				t.Error("New() returned nil GitOps without error")
			}
		})
	}
}

func TestAddFiles(t *testing.T) {
	repoPath := initTestRepo(t)

	g, err := gitops.New(repoPath, "", "")
	if err != nil {
		t.Fatalf("failed to create GitOps: %v", err)
	}

	// Create a test file
	testFile := filepath.Join(repoPath, "test.yaml")
	if err := os.WriteFile(testFile, []byte("test: content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	tests := []struct {
		name    string
		files   []string
		wantErr bool
	}{
		{
			name:    "add existing file",
			files:   []string{testFile},
			wantErr: false,
		},
		{
			name:    "add nonexistent file",
			files:   []string{filepath.Join(repoPath, "nonexistent.yaml")},
			wantErr: true,
		},
		{
			name:    "add empty list",
			files:   []string{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.AddFiles(tt.files)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddFiles() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCommit(t *testing.T) {
	repoPath := initTestRepo(t)

	g, err := gitops.New(repoPath, "", "")
	if err != nil {
		t.Fatalf("failed to create GitOps: %v", err)
	}

	// Create and stage a test file
	testFile := filepath.Join(repoPath, "commit-test.yaml")
	if err := os.WriteFile(testFile, []byte("test: content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := g.AddFiles([]string{testFile}); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	tests := []struct {
		name        string
		message     string
		authorName  string
		authorEmail string
		wantErr     bool
	}{
		{
			name:        "commit with full author info",
			message:     "test commit",
			authorName:  "Test Author",
			authorEmail: "test@example.com",
			wantErr:     false,
		},
		{
			name:        "commit with defaults",
			message:     "another commit",
			authorName:  "",
			authorEmail: "",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new file for each test to ensure there's something to commit
			newFile := filepath.Join(repoPath, tt.name+".yaml")
			if err := os.WriteFile(newFile, []byte("content: "+tt.name), 0644); err != nil {
				t.Fatalf("failed to create test file: %v", err)
			}
			if err := g.AddFiles([]string{newFile}); err != nil {
				t.Fatalf("failed to add file: %v", err)
			}

			err := g.Commit(tt.message, tt.authorName, tt.authorEmail)
			if (err != nil) != tt.wantErr {
				t.Errorf("Commit() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPush(t *testing.T) {
	repoPath := initTestRepo(t)

	tests := []struct {
		name    string
		user    string
		token   string
		remote  string
		branch  string
		wantErr bool
	}{
		{
			name:    "push without auth should fail",
			user:    "",
			token:   "",
			remote:  "origin",
			branch:  "main",
			wantErr: true,
		},
		{
			name:    "push with auth but invalid remote",
			user:    "testuser",
			token:   "testtoken",
			remote:  "nonexistent",
			branch:  "main",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := gitops.New(repoPath, tt.user, tt.token)
			if err != nil {
				t.Fatalf("failed to create GitOps: %v", err)
			}

			err = g.Push(tt.remote, tt.branch)
			if (err != nil) != tt.wantErr {
				t.Errorf("Push() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCleanup(t *testing.T) {
	repoPath := initTestRepo(t)

	g, err := gitops.New(repoPath, "", "")
	if err != nil {
		t.Fatalf("failed to create GitOps: %v", err)
	}

	// Create a subdirectory with files
	subdir := filepath.Join(repoPath, "subdir")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	err = g.Cleanup()
	if err != nil {
		t.Errorf("Cleanup() error = %v", err)
	}

	// Verify directory is removed
	if _, err := os.Stat(repoPath); !os.IsNotExist(err) {
		t.Error("Cleanup() did not remove directory")
	}
}

func TestGetRepo(t *testing.T) {
	repoPath := initTestRepo(t)

	g, err := gitops.New(repoPath, "", "")
	if err != nil {
		t.Fatalf("failed to create GitOps: %v", err)
	}

	repo := g.GetRepo()
	if repo == nil {
		t.Error("GetRepo() returned nil")
	}
}

func TestConfig(t *testing.T) {
	config := gitops.Config{
		RepoPath:     "/test/path",
		RepoURL:      "https://github.com/test/repo",
		Branch:       "feature-branch",
		CreateBranch: true,
		Remote:       "origin",
		User:         "testuser",
		Token:        "testtoken",
		CommitMsg:    "test commit message",
	}

	if config.RepoPath != "/test/path" {
		t.Errorf("unexpected RepoPath: %s", config.RepoPath)
	}
	if config.Branch != "feature-branch" {
		t.Errorf("unexpected Branch: %s", config.Branch)
	}
	if !config.CreateBranch {
		t.Error("expected CreateBranch to be true")
	}
}

// initTestRepo creates a temporary git repository for testing
func initTestRepo(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()

	repo, err := git.PlainInit(tmpDir, false)
	if err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}

	// Create an initial commit to establish HEAD
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatalf("failed to get worktree: %v", err)
	}

	// Create a file and commit it
	readmePath := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test Repo"), 0644); err != nil {
		t.Fatalf("failed to create README: %v", err)
	}

	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatalf("failed to add README: %v", err)
	}

	_, err = worktree.Commit("Initial commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	return tmpDir
}
