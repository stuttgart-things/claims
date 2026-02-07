package cmd

import (
	"fmt"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	deleteResourceName string
	deleteCategory     string
	deleteRepoURL      string
	deleteRegistryPath string
	deleteDryRun       bool

	// Git flags for delete (reuse same env vars)
	deleteGitBranch       string
	deleteGitCreateBranch bool
	deleteGitMessage      string
	deleteGitRemote       string
	deleteGitUser         string
	deleteGitToken        string

	// PR flags for delete
	deleteCreatePR      bool
	deletePRTitle       string
	deletePRDescription string
	deletePRLabels      []string
	deletePRBase        string

	// Mode flags for delete
	deleteInteractive    bool
	deleteNonInteractive bool
)

var deleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a claim via Git PR",
	Long:  `Removes a Crossplane claim by deleting its directory, updating kustomization.yaml, and removing the registry entry. Creates a Git PR for the change.`,
	Run:   runDelete,
}

func init() {
	deleteCmd.Flags().StringVar(&deleteResourceName, "resource-name", "", "Name of the claim resource to delete")
	deleteCmd.Flags().StringVar(&deleteCategory, "category", "", "Category of the claim (e.g., infra, apps)")
	deleteCmd.Flags().StringVar(&deleteRepoURL, "git-repo-url", "", "Clone from URL instead of using local repo")
	deleteCmd.Flags().StringVar(&deleteRegistryPath, "registry-path", "claims/registry.yaml", "Path to registry.yaml within the repo")
	deleteCmd.Flags().BoolVar(&deleteDryRun, "dry-run", false, "Show what would be deleted without making changes")

	// Git flags
	deleteCmd.Flags().StringVar(&deleteGitBranch, "git-branch", "", "Branch to use/create")
	deleteCmd.Flags().BoolVar(&deleteGitCreateBranch, "git-create-branch", false, "Create the branch if it doesn't exist")
	deleteCmd.Flags().StringVar(&deleteGitMessage, "git-message", "", "Commit message (default: auto-generated)")
	deleteCmd.Flags().StringVar(&deleteGitRemote, "git-remote", "origin", "Git remote name")
	deleteCmd.Flags().StringVar(&deleteGitUser, "git-user", "", "Git username (or GIT_USER/GITHUB_USER env)")
	deleteCmd.Flags().StringVar(&deleteGitToken, "git-token", "", "Git token (or GIT_TOKEN/GITHUB_TOKEN env)")

	// PR flags
	deleteCmd.Flags().BoolVar(&deleteCreatePR, "create-pr", false, "Create a pull request after push")
	deleteCmd.Flags().StringVar(&deletePRTitle, "pr-title", "", "PR title (default: auto-generated)")
	deleteCmd.Flags().StringVar(&deletePRDescription, "pr-description", "", "PR description")
	deleteCmd.Flags().StringSliceVar(&deletePRLabels, "pr-labels", nil, "PR labels (comma-separated)")
	deleteCmd.Flags().StringVar(&deletePRBase, "pr-base", "main", "Base branch for PR")

	// Mode flags
	deleteCmd.Flags().BoolVarP(&deleteInteractive, "interactive", "i", false, "Force interactive mode")
	deleteCmd.Flags().BoolVar(&deleteNonInteractive, "non-interactive", false, "Force non-interactive mode")

	rootCmd.AddCommand(deleteCmd)
}

func runDelete(cmd *cobra.Command, args []string) {
	fmt.Println(logo)

	config := &DeleteConfig{
		ResourceName: deleteResourceName,
		Category:     deleteCategory,
		RepoURL:      deleteRepoURL,
		RegistryPath: deleteRegistryPath,
		DryRun:       deleteDryRun,
	}

	// Build git config
	if deleteGitBranch != "" || deleteRepoURL != "" || deleteCreatePR {
		config.GitConfig = &GitConfig{
			Commit:       true,
			Push:         true,
			CreateBranch: deleteGitCreateBranch,
			Message:      deleteGitMessage,
			Branch:       deleteGitBranch,
			Remote:       deleteGitRemote,
			RepoURL:      deleteRepoURL,
			User:         deleteGitUser,
			Token:        deleteGitToken,
		}
	}

	// Build PR config
	if deleteCreatePR || deletePRTitle != "" || deletePRDescription != "" || len(deletePRLabels) > 0 {
		config.PRConfig = &PRConfig{
			Create:      deleteCreatePR,
			Title:       deletePRTitle,
			Description: deletePRDescription,
			Labels:      deletePRLabels,
			BaseBranch:  deletePRBase,
		}
	}

	// Determine mode
	if deleteNonInteractive {
		config.Interactive = false
	} else if deleteInteractive {
		config.Interactive = true
	} else {
		config.Interactive = isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	}

	var err error
	if config.Interactive {
		err = runDeleteInteractive(config)
	} else {
		err = runDeleteNonInteractive(config)
	}

	if err != nil {
		fmt.Println(errorStyle.Render(err.Error()))
		os.Exit(1)
	}
}
