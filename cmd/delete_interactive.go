package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/stuttgart-things/claims/internal/registry"
)

// runDeleteInteractive runs the delete command in interactive mode
func runDeleteInteractive(config *DeleteConfig) error {
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

	if len(reg.Claims) == 0 {
		fmt.Println("No claims found in registry.")
		return nil
	}

	// Build select options from registry
	var options []huh.Option[string]
	for _, entry := range reg.Claims {
		label := fmt.Sprintf("%s (%s/%s) [%s]", entry.Name, entry.Category, entry.Template, entry.Status)
		options = append(options, huh.NewOption(label, entry.Name))
	}

	var selected string
	selectForm := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select claim to delete").
				Description("Choose the claim to remove").
				Options(options...).
				Value(&selected),
		),
	)

	if err := selectForm.Run(); err != nil {
		return fmt.Errorf("selection form: %w", err)
	}

	entry := registry.FindEntry(reg, selected)
	if entry == nil {
		return fmt.Errorf("claim %q not found in registry", selected)
	}

	// Show what will be deleted
	fmt.Printf("\nClaim to delete:\n")
	fmt.Printf("  Name:       %s\n", entry.Name)
	fmt.Printf("  Template:   %s\n", entry.Template)
	fmt.Printf("  Category:   %s\n", entry.Category)
	fmt.Printf("  Namespace:  %s\n", entry.Namespace)
	fmt.Printf("  Path:       %s\n", entry.Path)
	fmt.Printf("  Created by: %s\n", entry.CreatedBy)
	fmt.Println()

	// Confirm
	var confirm bool
	confirmForm := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Delete claim %q?", selected)).
				Description("This will remove the claim directory, update kustomization.yaml, and update registry.yaml").
				Affirmative("Yes, delete").
				Negative("Cancel").
				Value(&confirm),
		),
	)

	if err := confirmForm.Run(); err != nil {
		return fmt.Errorf("confirmation form: %w", err)
	}

	if !confirm {
		fmt.Println("Cancelled.")
		return nil
	}

	if config.DryRun {
		return printDeleteDryRun(entry.Name, entry.Category, entry.Path, repoRoot)
	}

	// Perform the deletion
	result, err := performDelete(repoRoot, config.RegistryPath, entry.Name, entry.Category)
	if err != nil {
		return err
	}

	fmt.Println(successStyle.Render(fmt.Sprintf("\nDeleted claim: %s", result.ResourceName)))

	// Ask about git operations if not already configured
	if config.GitConfig == nil {
		destChoice, err := runDeleteDestinationChoice()
		if err != nil {
			return fmt.Errorf("destination choice: %w", err)
		}

		if destChoice.useGit {
			gitConfig, err := runGitDetailsForm(destChoice.createPR)
			if err != nil {
				return fmt.Errorf("git options: %w", err)
			}
			config.GitConfig = gitConfig

			if destChoice.createPR {
				prConfig, err := runPROptionsForm()
				if err != nil {
					return fmt.Errorf("PR options: %w", err)
				}
				config.PRConfig = prConfig
			}
		}
	}

	// Execute git operations
	if config.GitConfig != nil {
		if err := executeDeleteGitOperations(result, config, repoRoot); err != nil {
			return fmt.Errorf("git operations: %w", err)
		}
	}

	return nil
}

// runDeleteDestinationChoice asks whether to commit+push+PR
func runDeleteDestinationChoice() (destinationChoice, error) {
	var destination string

	// Check if we're in a git repo
	cwd, err := os.Getwd()
	if err != nil {
		return destinationChoice{}, nil
	}
	if _, err := findRepoRoot(cwd); err != nil {
		return destinationChoice{}, nil
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Create a Git PR for this deletion?").
				Description("Choose how to handle the changes").
				Options(
					huh.NewOption("Create PR (commit, push & create PR)", "pr"),
					huh.NewOption("Keep local changes only", "local"),
				).
				Value(&destination),
		),
	)

	if err := form.Run(); err != nil {
		return destinationChoice{}, err
	}

	return destinationChoice{
		useGit:   destination == "pr",
		createPR: destination == "pr",
	}, nil
}
