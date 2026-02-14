package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stuttgart-things/claims/internal/gitops"
	"github.com/stuttgart-things/claims/internal/registry"
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

	// Also stage registry.yaml if it was updated
	repoPath := g.RepoPath
	registryPath := filepath.Join(repoPath, "claims", "registry.yaml")
	if _, err := os.Stat(registryPath); err == nil {
		filePaths = append(filePaths, registryPath)
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

		// Get branch name to push
		branch := config.GitConfig.Branch
		if branch == "" {
			branch, err = g.GetCurrentBranch()
			if err != nil {
				return fmt.Errorf("getting current branch: %w", err)
			}
		}

		fmt.Printf("Pushing to %s...\n", remote)
		if err := g.Push(remote, branch); err != nil {
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

// updateRegistryForRender adds entries to claims/registry.yaml for successful renders
func updateRegistryForRender(results []RenderResult, config *RenderConfig) {
	// Try to find repo root from output directory
	repoRoot, err := findRepoRoot(config.OutputDir)
	if err != nil {
		return // Not in a git repo, skip registry update
	}

	registryPath := filepath.Join(repoRoot, "claims", "registry.yaml")

	// Load or create registry
	reg, err := registry.Load(registryPath)
	if err != nil {
		// Create new registry if file doesn't exist
		if !os.IsNotExist(err) {
			return
		}
		reg = registry.NewRegistry()
	}

	// Determine repository name from git remote (best effort)
	repoName := ""
	if config.GitConfig != nil && config.GitConfig.RepoURL != "" {
		repoName = config.GitConfig.RepoURL
	}
	if repoName == "" {
		// Try to read remote URL from local repo
		g, err := gitops.New(repoRoot, "", "")
		if err == nil {
			if url, err := g.GetRemoteURL("origin"); err == nil {
				repoName = extractRepoSlug(url)
			}
		}
	}

	// Resolve git user for createdBy
	createdBy := "cli"
	if config.GitConfig != nil && config.GitConfig.User != "" {
		createdBy = config.GitConfig.User
	}

	// Compute category from output directory relative to claims/
	category := ""
	absOutputDir, _ := filepath.Abs(config.OutputDir)
	relOut, err := filepath.Rel(filepath.Join(repoRoot, "claims"), absOutputDir)
	if err == nil && relOut != ".." && !strings.HasPrefix(relOut, "..") {
		parts := strings.SplitN(relOut, string(filepath.Separator), 2)
		if len(parts) > 0 && parts[0] != "." {
			category = parts[0]
		}
	}

	updated := false
	for _, r := range results {
		if r.Error != nil || r.OutputPath == "" {
			continue
		}

		// Compute path relative to repo root
		absOutPath, _ := filepath.Abs(r.OutputPath)
		relPath, err := filepath.Rel(repoRoot, absOutPath)
		if err != nil {
			relPath = r.OutputPath
		}

		entry := registry.ClaimEntry{
			Name:       r.ResourceName,
			Template:   r.TemplateName,
			Category:   category,
			CreatedAt:  time.Now().UTC().Format(time.RFC3339),
			CreatedBy:  createdBy,
			Source:     "cli",
			Repository: repoName,
			Path:       relPath,
			Status:     "active",
		}

		registry.AddEntry(reg, entry)
		updated = true
	}

	if updated {
		// Ensure claims directory exists
		if err := os.MkdirAll(filepath.Dir(registryPath), 0755); err != nil {
			return
		}
		if err := registry.Save(registryPath, reg); err != nil {
			fmt.Printf("Warning: could not update registry: %v\n", err)
		}
	}
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

// extractRepoSlug extracts "owner/repo" from a git remote URL.
// Supports both HTTPS (https://github.com/owner/repo.git) and SSH (git@github.com:owner/repo.git).
func extractRepoSlug(url string) string {
	// Remove trailing .git
	url = strings.TrimSuffix(url, ".git")

	// SSH format: git@github.com:owner/repo
	if idx := strings.Index(url, ":"); strings.Contains(url, "@") && idx > 0 {
		slug := url[idx+1:]
		return slug
	}

	// HTTPS format: https://github.com/owner/repo
	parts := strings.Split(url, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}

	return url
}
