package gitops

import (
	"fmt"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// CreateBranch creates and checks out a new branch
func (g *GitOps) CreateBranch(name string) error {
	worktree, err := g.repo.Worktree()
	if err != nil {
		return fmt.Errorf("getting worktree: %w", err)
	}

	// Get current HEAD
	headRef, err := g.repo.Head()
	if err != nil {
		return fmt.Errorf("getting HEAD: %w", err)
	}

	// Create new branch reference
	branchRef := plumbing.NewBranchReferenceName(name)
	ref := plumbing.NewHashReference(branchRef, headRef.Hash())

	if err := g.repo.Storer.SetReference(ref); err != nil {
		return fmt.Errorf("creating branch: %w", err)
	}

	// Checkout the new branch, keeping untracked files
	if err := worktree.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
		Keep:   true, // Preserve untracked files (like newly rendered manifests)
	}); err != nil {
		return fmt.Errorf("checking out branch: %w", err)
	}

	return nil
}

// CheckoutBranch checks out an existing branch
func (g *GitOps) CheckoutBranch(name string) error {
	worktree, err := g.repo.Worktree()
	if err != nil {
		return fmt.Errorf("getting worktree: %w", err)
	}

	branchRef := plumbing.NewBranchReferenceName(name)
	if err := worktree.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
		Keep:   true, // Preserve untracked files (like newly rendered manifests)
	}); err != nil {
		return fmt.Errorf("checking out branch: %w", err)
	}

	return nil
}

// GetCurrentBranch returns the name of the current branch
func (g *GitOps) GetCurrentBranch() (string, error) {
	head, err := g.repo.Head()
	if err != nil {
		return "", fmt.Errorf("getting HEAD: %w", err)
	}

	return head.Name().Short(), nil
}
