package cmd

import (
	"fmt"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/stuttgart-things/claims/internal/gitops"
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

	// XR deletion (claims#93)
	deleteXR deleteXRFlags
)

type deleteXRFlags struct {
	DeleteXRConfig
	pick bool
}

var deleteCmd = &cobra.Command{
	Use:     "delete",
	GroupID: groupClaims,
	Short:   "Delete a claim via Git PR",
	Long: `Removes a Crossplane claim through a Git PR. Two layouts:

XR in an *-xrs directory (clusters/<lab>/<infra>/<x>-xrs/, e.g. machinery orders):
  name it with --kind/--name or --source-path (interactive: pick it with --xr,
  or automatically when the repository has no claims registry). The claim's
  file, the run-once runs that apply to it and their kustomization.yaml
  entries are removed on the branch delete-claim/<name>, and with --create-pr
  the PR "Delete claim: <Kind>/<name>" (label delete-claim) is opened. The
  fleet repository's pr-claim-delete.yaml gates it; after the merge
  delete-claim-xr.yaml deletes the object. claims never deletes anything on a
  cluster.

Registry claim (claims/<category>/<name>): removes the directory, its
  kustomization.yaml entry and the registry.yaml entry.`,
	Example: `  # XR: file-first deletion PR
  claims delete --kind NativeVsphereVM --name dev7-vm --create-pr --reason "project ended"
  claims delete --source-path clusters/labda/vsphere/machinery-orders-xrs/dev7.yaml --dry-run
  claims delete --xr            # pick an XR interactively`,
	Run: runDelete,
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

	// XR flags
	deleteCmd.Flags().StringVar(&deleteXR.Kind, "kind", "", "XR kind, e.g. NativeVsphereVM (XR deletion)")
	deleteCmd.Flags().StringVar(&deleteXR.Name, "name", "", "XR name (metadata.name)")
	deleteCmd.Flags().StringVar(&deleteXR.Namespace, "namespace", "default", "XR namespace")
	deleteCmd.Flags().StringVar(&deleteXR.SourcePath, "source-path", "", "File the XR lives in (its stuttgart-things.com/source-path)")
	deleteCmd.Flags().StringVar(&deleteXR.Reason, "reason", "", "Why the claim can go (PR body)")
	deleteCmd.Flags().StringVar(&deleteXR.RequestedBy, "requested-by", "", "Who asks for the deletion (PR body; default: the git user)")
	deleteCmd.Flags().BoolVar(&deleteXR.pick, "xr", false, "Pick an XR from the *-xrs directories interactively")

	rootCmd.AddCommand(deleteCmd)
}

func runDelete(cmd *cobra.Command, args []string) {

	config := &DeleteConfig{
		ResourceName: deleteResourceName,
		Category:     deleteCategory,
		RepoURL:      deleteRepoURL,
		RegistryPath: deleteRegistryPath,
		DryRun:       deleteDryRun,
		PickXR:       deleteXR.pick,
	}
	xr := deleteXR.DeleteXRConfig
	if xr.RequestedBy == "" {
		xr.RequestedBy, _ = gitops.ResolveCredentialsOptional(deleteGitUser, deleteGitToken)
	}
	config.XR = &xr

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
	switch {
	case config.xrMode() && config.Interactive:
		err = runDeleteXR(config, pickXRInteractive)
	case config.xrMode():
		err = runDeleteXR(config, nil)
	case config.Interactive && (config.PickXR || xrRepo(config)):
		err = runDeleteXR(config, pickXRInteractive)
	case config.PickXR:
		err = fmt.Errorf("--xr picks interactively; without a terminal name the claim with --kind/--name or --source-path")
	case config.Interactive:
		err = runDeleteInteractive(config)
	default:
		err = runDeleteNonInteractive(config)
	}

	exitOn(err)
}

// xrRepo reports whether the current repository has *-xrs directories but no
// claims registry, so an interactive delete offers XRs.
func xrRepo(config *DeleteConfig) bool {
	if config.RepoURL != "" {
		return false
	}
	root, err := resolveRepoRoot(config)
	return err == nil && registryMissing(root, config.RegistryPath)
}
