package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stuttgart-things/claims/internal/gitops"
)

// executeGitOperations performs git commit and push if configured
func executeGitOperations(results []RenderResult, config *RenderConfig) error {
	if config.GitConfig == nil || (!config.GitConfig.Commit && !config.GitConfig.Push) {
		return nil
	}

	// Resolve credentials if pushing
	user, token := config.GitConfig.User, config.GitConfig.Token
	if config.GitConfig.Push {
		var err error
		user, token, err = gitops.ResolveCredentials(user, token)
		if err != nil {
			return err
		}
	} else {
		// For commit only, credentials are optional
		user, token = gitops.ResolveCredentialsOptional(user, token)
	}

	var g *gitops.GitOps
	var tmpDir string
	var err error

	// Clone-based or local workflow
	if config.GitConfig.RepoURL != "" {
		fmt.Printf("Cloning %s...\n", config.GitConfig.RepoURL)
		g, tmpDir, err = gitops.Clone(config.GitConfig.RepoURL, user, token)
		if err != nil {
			return err
		}
		defer g.Cleanup()

		// Adjust output directory to be inside cloned repo
		config.OutputDir = filepath.Join(tmpDir, config.OutputDir)
	} else {
		// Find repo root from output directory
		repoPath, err := findRepoRoot(config.OutputDir)
		if err != nil {
			return fmt.Errorf("output directory is not in a git repository: %w", err)
		}
		g, err = gitops.New(repoPath, user, token)
		if err != nil {
			return err
		}
	}

	// Create branch if requested
	if config.GitConfig.CreateBranch && config.GitConfig.Branch != "" {
		fmt.Printf("Creating branch: %s\n", config.GitConfig.Branch)
		if err := g.CreateBranch(config.GitConfig.Branch); err != nil {
			return err
		}
	} else if config.GitConfig.Branch != "" {
		fmt.Printf("Checking out branch: %s\n", config.GitConfig.Branch)
		if err := g.CheckoutBranch(config.GitConfig.Branch); err != nil {
			return err
		}
	}

	// Collect file paths
	var filePaths []string
	for _, r := range results {
		if r.OutputPath != "" && r.Error == nil {
			filePaths = append(filePaths, r.OutputPath)
		}
	}

	if len(filePaths) == 0 {
		return fmt.Errorf("no files to commit")
	}

	// Stage files
	fmt.Println("Staging files...")
	if err := g.AddFiles(filePaths); err != nil {
		return err
	}

	// Generate commit message
	message := config.GitConfig.Message
	if message == "" {
		var names []string
		for _, r := range results {
			if r.Error == nil {
				names = append(names, r.TemplateName)
			}
		}
		message = fmt.Sprintf("Rendered claims: %s", strings.Join(names, ", "))
	}

	// Commit
	fmt.Printf("Committing: %s\n", message)
	if err := g.Commit(message, user, ""); err != nil {
		return err
	}
	fmt.Println(successStyle.Render("Committed successfully"))

	// Push if requested
	if config.GitConfig.Push {
		remote := config.GitConfig.Remote
		if remote == "" {
			remote = "origin"
		}
		fmt.Printf("Pushing to %s...\n", remote)
		if err := g.Push(remote); err != nil {
			return err
		}
		fmt.Println(successStyle.Render("Pushed successfully"))

		// Create PR if requested (after successful push)
		if config.PRConfig != nil && config.PRConfig.Create {
			repoPath := g.RepoPath
			if err := executePRCreation(results, config, repoPath); err != nil {
				return fmt.Errorf("creating pull request: %w", err)
			}
		}
	}

	return nil
}

// findRepoRoot finds the git repository root from a starting path
func findRepoRoot(startPath string) (string, error) {
	absPath, err := filepath.Abs(startPath)
	if err != nil {
		return "", err
	}

	for {
		gitDir := filepath.Join(absPath, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			return absPath, nil
		}

		parent := filepath.Dir(absPath)
		if parent == absPath {
			return "", fmt.Errorf("not a git repository")
		}
		absPath = parent
	}
}
