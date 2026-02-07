package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/stuttgart-things/claims/internal/kustomize"
	"github.com/stuttgart-things/claims/internal/registry"
)

// runDeleteNonInteractive runs the delete command in non-interactive mode
func runDeleteNonInteractive(config *DeleteConfig) error {
	if config.ResourceName == "" {
		return fmt.Errorf("--resource-name is required in non-interactive mode")
	}

	// Determine repo root
	repoRoot, err := resolveRepoRoot(config)
	if err != nil {
		return err
	}

	registryPath := filepath.Join(repoRoot, config.RegistryPath)

	// Load registry
	reg, err := registry.Load(registryPath)
	if err != nil {
		return fmt.Errorf("loading registry: %w", err)
	}

	// Find the claim
	entry := registry.FindEntry(reg, config.ResourceName)
	if entry == nil {
		return fmt.Errorf("claim %q not found in registry", config.ResourceName)
	}

	// Use category from registry if not provided
	category := config.Category
	if category == "" {
		category = entry.Category
	}

	if config.DryRun {
		return printDeleteDryRun(config.ResourceName, category, entry.Path, repoRoot)
	}

	result, err := performDelete(repoRoot, config.RegistryPath, config.ResourceName, category)
	if err != nil {
		return err
	}

	fmt.Println(successStyle.Render(fmt.Sprintf("Deleted claim: %s", result.ResourceName)))

	// Execute git operations
	if config.GitConfig != nil {
		if err := executeDeleteGitOperations(result, config, repoRoot); err != nil {
			return fmt.Errorf("git operations: %w", err)
		}
	}

	return nil
}

// resolveRepoRoot determines the repository root path
func resolveRepoRoot(config *DeleteConfig) (string, error) {
	if config.RepoURL != "" {
		// Clone-based workflow is handled in git operations
		return "", fmt.Errorf("clone-based workflow requires git configuration; use --git-repo-url with git flags")
	}

	// Use current directory as starting point
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}

	repoRoot, err := findRepoRoot(cwd)
	if err != nil {
		return "", fmt.Errorf("not in a git repository: %w", err)
	}

	return repoRoot, nil
}

// performDelete removes the claim directory, updates kustomization.yaml, and updates registry.yaml
func performDelete(repoRoot, registryRelPath, resourceName, category string) (*DeleteResult, error) {
	claimDir := filepath.Join(repoRoot, "claims", category, resourceName)
	kustomizationPath := filepath.Join(repoRoot, "claims", category, "kustomization.yaml")
	registryPath := filepath.Join(repoRoot, registryRelPath)

	// Verify claim directory exists
	if _, err := os.Stat(claimDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("claim directory not found: %s", claimDir)
	}

	// Remove claim directory
	if err := os.RemoveAll(claimDir); err != nil {
		return nil, fmt.Errorf("removing claim directory: %w", err)
	}
	fmt.Printf("Removed directory: %s\n", claimDir)

	// Update kustomization.yaml
	if _, err := os.Stat(kustomizationPath); err == nil {
		k, err := kustomize.Load(kustomizationPath)
		if err != nil {
			return nil, fmt.Errorf("loading kustomization: %w", err)
		}

		if err := kustomize.RemoveResource(k, resourceName); err != nil {
			fmt.Printf("Warning: %v\n", err)
		} else {
			if err := kustomize.Save(kustomizationPath, k); err != nil {
				return nil, fmt.Errorf("saving kustomization: %w", err)
			}
			fmt.Printf("Updated kustomization: %s\n", kustomizationPath)
		}
	}

	// Update registry.yaml
	reg, err := registry.Load(registryPath)
	if err != nil {
		return nil, fmt.Errorf("loading registry: %w", err)
	}

	if err := registry.RemoveEntry(reg, resourceName); err != nil {
		fmt.Printf("Warning: %v\n", err)
	} else {
		if err := registry.Save(registryPath, reg); err != nil {
			return nil, fmt.Errorf("saving registry: %w", err)
		}
		fmt.Printf("Updated registry: %s\n", registryPath)
	}

	return &DeleteResult{
		ResourceName: resourceName,
		Category:     category,
		Path:         filepath.Join("claims", category, resourceName),
	}, nil
}

// printDeleteDryRun shows what would be deleted
func printDeleteDryRun(resourceName, category, path, repoRoot string) error {
	fmt.Println("\n=== DRY RUN - No changes made ===")
	fmt.Printf("Would delete claim: %s\n", resourceName)
	fmt.Printf("  Category:    %s\n", category)
	fmt.Printf("  Directory:   %s\n", filepath.Join(repoRoot, "claims", category, resourceName))
	fmt.Printf("  Registry:    remove entry from registry.yaml\n")
	fmt.Printf("  Kustomize:   remove resource from claims/%s/kustomization.yaml\n", category)
	return nil
}
