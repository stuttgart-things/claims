package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// GitOps handles git operations for the claims CLI
type GitOps struct {
	RepoPath string
	repo     *git.Repository
	auth     *http.BasicAuth
}

// Config holds git-related configuration
type Config struct {
	RepoPath     string
	RepoURL      string // For clone-based workflow
	Branch       string
	CreateBranch bool
	Remote       string
	User         string
	Token        string
	CommitMsg    string
}

// New creates a GitOps instance for an existing repo
func New(repoPath string, user, token string) (*GitOps, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("opening repository: %w", err)
	}

	g := &GitOps{
		RepoPath: repoPath,
		repo:     repo,
	}

	if user != "" && token != "" {
		g.auth = &http.BasicAuth{
			Username: user,
			Password: token,
		}
	}

	return g, nil
}

// Clone clones a repository to a temp directory
func Clone(url, user, token string) (*GitOps, string, error) {
	tmpDir, err := os.MkdirTemp("", "claims-gitops-*")
	if err != nil {
		return nil, "", fmt.Errorf("creating temp directory: %w", err)
	}

	var auth *http.BasicAuth
	if user != "" && token != "" {
		auth = &http.BasicAuth{
			Username: user,
			Password: token,
		}
	}

	cloneOpts := &git.CloneOptions{
		URL:      url,
		Progress: os.Stdout,
	}
	if auth != nil {
		cloneOpts.Auth = auth
	}

	repo, err := git.PlainClone(tmpDir, false, cloneOpts)
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, "", fmt.Errorf("cloning repository: %w", err)
	}

	return &GitOps{
		RepoPath: tmpDir,
		repo:     repo,
		auth:     auth,
	}, tmpDir, nil
}

// AddFiles stages files for commit
func (g *GitOps) AddFiles(files []string) error {
	worktree, err := g.repo.Worktree()
	if err != nil {
		return fmt.Errorf("getting worktree: %w", err)
	}

	for _, f := range files {
		// Convert absolute path to relative
		relPath, err := filepath.Rel(g.RepoPath, f)
		if err != nil {
			relPath = f
		}

		// Verify file exists on disk before staging
		absPath := filepath.Join(g.RepoPath, relPath)
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			return fmt.Errorf("file not found: %s", absPath)
		}

		// Use AddGlob which is more reliable for new files
		if err := worktree.AddGlob(relPath); err != nil {
			return fmt.Errorf("staging %s: %w", relPath, err)
		}
	}

	return nil
}

// Commit creates a commit with the staged changes
func (g *GitOps) Commit(message, authorName, authorEmail string) error {
	worktree, err := g.repo.Worktree()
	if err != nil {
		return fmt.Errorf("getting worktree: %w", err)
	}

	if authorName == "" {
		authorName = "claims-cli"
	}
	if authorEmail == "" {
		authorEmail = "claims-cli@automated"
	}

	_, err = worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  authorName,
			Email: authorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("committing: %w", err)
	}

	return nil
}

// Push pushes to remote
func (g *GitOps) Push(remote string) error {
	if g.auth == nil {
		return fmt.Errorf("git credentials required for push")
	}

	err := g.repo.Push(&git.PushOptions{
		RemoteName: remote,
		Auth:       g.auth,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("pushing: %w", err)
	}

	return nil
}

// Cleanup removes the repository directory (for clone-based workflows)
func (g *GitOps) Cleanup() error {
	return os.RemoveAll(g.RepoPath)
}

// GetRepo returns the underlying git repository
func (g *GitOps) GetRepo() *git.Repository {
	return g.repo
}
