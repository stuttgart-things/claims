package gitops_test

import (
	"testing"

	"github.com/stuttgart-things/claims/internal/gitops"
)

func TestCreateBranch(t *testing.T) {
	repoPath := initTestRepo(t)

	g, err := gitops.New(repoPath, "", "")
	if err != nil {
		t.Fatalf("failed to create GitOps: %v", err)
	}

	tests := []struct {
		name       string
		branchName string
		wantErr    bool
	}{
		{
			name:       "create valid branch",
			branchName: "feature/test-branch",
			wantErr:    false,
		},
		{
			name:       "create branch with simple name",
			branchName: "test-branch",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.CreateBranch(tt.branchName)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateBranch() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				// Verify branch was created and checked out
				currentBranch, err := g.GetCurrentBranch()
				if err != nil {
					t.Errorf("failed to get current branch: %v", err)
				}
				if currentBranch != tt.branchName {
					t.Errorf("expected branch %s, got %s", tt.branchName, currentBranch)
				}
			}
		})
	}
}

func TestCheckoutBranch(t *testing.T) {
	repoPath := initTestRepo(t)

	g, err := gitops.New(repoPath, "", "")
	if err != nil {
		t.Fatalf("failed to create GitOps: %v", err)
	}

	// Create a branch first
	if err := g.CreateBranch("checkout-test"); err != nil {
		t.Fatalf("failed to create test branch: %v", err)
	}

	// Go back to master/main
	originalBranch, _ := g.GetCurrentBranch()

	tests := []struct {
		name       string
		branchName string
		wantErr    bool
	}{
		{
			name:       "checkout existing branch",
			branchName: "checkout-test",
			wantErr:    false,
		},
		{
			name:       "checkout nonexistent branch",
			branchName: "nonexistent-branch",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// First go back to original branch
			_ = g.CheckoutBranch(originalBranch)

			err := g.CheckoutBranch(tt.branchName)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckoutBranch() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				currentBranch, err := g.GetCurrentBranch()
				if err != nil {
					t.Errorf("failed to get current branch: %v", err)
				}
				if currentBranch != tt.branchName {
					t.Errorf("expected branch %s, got %s", tt.branchName, currentBranch)
				}
			}
		})
	}
}

func TestGetCurrentBranch(t *testing.T) {
	repoPath := initTestRepo(t)

	g, err := gitops.New(repoPath, "", "")
	if err != nil {
		t.Fatalf("failed to create GitOps: %v", err)
	}

	branch, err := g.GetCurrentBranch()
	if err != nil {
		t.Errorf("GetCurrentBranch() error = %v", err)
	}

	// The default branch after init is usually "master" or could be "main"
	if branch == "" {
		t.Error("GetCurrentBranch() returned empty string")
	}

	// Create and switch to a new branch
	newBranch := "test-get-current"
	if err := g.CreateBranch(newBranch); err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	branch, err = g.GetCurrentBranch()
	if err != nil {
		t.Errorf("GetCurrentBranch() after switch error = %v", err)
	}
	if branch != newBranch {
		t.Errorf("expected branch %s, got %s", newBranch, branch)
	}
}

func TestBranchWorkflow(t *testing.T) {
	repoPath := initTestRepo(t)

	g, err := gitops.New(repoPath, "", "")
	if err != nil {
		t.Fatalf("failed to create GitOps: %v", err)
	}

	// Get original branch
	originalBranch, err := g.GetCurrentBranch()
	if err != nil {
		t.Fatalf("failed to get original branch: %v", err)
	}

	// Create a feature branch
	featureBranch := "feature/new-feature"
	if err := g.CreateBranch(featureBranch); err != nil {
		t.Fatalf("CreateBranch() error = %v", err)
	}

	// Verify we're on the feature branch
	currentBranch, err := g.GetCurrentBranch()
	if err != nil {
		t.Fatalf("GetCurrentBranch() error = %v", err)
	}
	if currentBranch != featureBranch {
		t.Errorf("expected to be on %s, got %s", featureBranch, currentBranch)
	}

	// Switch back to original branch
	if err := g.CheckoutBranch(originalBranch); err != nil {
		t.Fatalf("CheckoutBranch() error = %v", err)
	}

	// Verify we're back on original
	currentBranch, err = g.GetCurrentBranch()
	if err != nil {
		t.Fatalf("GetCurrentBranch() error = %v", err)
	}
	if currentBranch != originalBranch {
		t.Errorf("expected to be on %s, got %s", originalBranch, currentBranch)
	}

	// Switch back to feature branch
	if err := g.CheckoutBranch(featureBranch); err != nil {
		t.Fatalf("CheckoutBranch() error = %v", err)
	}

	currentBranch, err = g.GetCurrentBranch()
	if err != nil {
		t.Fatalf("GetCurrentBranch() error = %v", err)
	}
	if currentBranch != featureBranch {
		t.Errorf("expected to be on %s, got %s", featureBranch, currentBranch)
	}
}
