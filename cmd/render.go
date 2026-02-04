package cmd

import (
	"fmt"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	apiURL          string
	outputDir       string
	dryRun          bool
	singleFile      bool
	filenamePattern string
	templateNames   []string

	// Non-interactive mode flags
	paramsFile     string
	inlineParams   []string
	interactive    bool
	nonInteractive bool

	// Git flags
	gitCommit       bool
	gitPush         bool
	gitBranch       string
	gitCreateBranch bool
	gitMessage      string
	gitRemote       string
	gitRepoURL      string
	gitUser         string
	gitToken        string

	// PR flags
	createPR      bool
	prTitle       string
	prDescription string
	prLabels      []string
	prBase        string
)

var renderCmd = &cobra.Command{
	Use:   "render",
	Short: "Render a claim template interactively",
	Long:  `Connects to the claim-machinery API, fetches available templates, and provides an interactive form to render claims.`,
	Run:   runRender,
}

func init() {
	renderCmd.Flags().StringVarP(&apiURL, "api-url", "a", "", "API URL (default: $CLAIM_API_URL or http://localhost:8080)")
	renderCmd.Flags().StringVarP(&outputDir, "output-dir", "o", ".", "Output directory for rendered files")
	renderCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print output without writing files")
	renderCmd.Flags().BoolVar(&singleFile, "single-file", false, "Combine all resources into one file")
	renderCmd.Flags().StringVar(&filenamePattern, "filename-pattern", "{{.template}}-{{.name}}.yaml", "Pattern for output filenames")
	renderCmd.Flags().StringSliceVarP(&templateNames, "templates", "t", nil, "Templates to render (comma-separated or repeated)")

	// Non-interactive mode flags
	renderCmd.Flags().StringVarP(&paramsFile, "params-file", "f", "", "YAML/JSON file with parameters")
	renderCmd.Flags().StringSliceVarP(&inlineParams, "param", "p", nil, "Inline param (key=value, repeatable)")
	renderCmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "Force interactive mode")
	renderCmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Force non-interactive mode")

	// Git flags
	renderCmd.Flags().BoolVar(&gitCommit, "git-commit", false, "Commit rendered files to git")
	renderCmd.Flags().BoolVar(&gitPush, "git-push", false, "Push commits to remote (implies --git-commit)")
	renderCmd.Flags().StringVar(&gitBranch, "git-branch", "", "Branch to use/create")
	renderCmd.Flags().BoolVar(&gitCreateBranch, "git-create-branch", false, "Create the branch if it doesn't exist")
	renderCmd.Flags().StringVar(&gitMessage, "git-message", "", "Commit message (default: auto-generated)")
	renderCmd.Flags().StringVar(&gitRemote, "git-remote", "origin", "Git remote name")
	renderCmd.Flags().StringVar(&gitRepoURL, "git-repo-url", "", "Clone from URL instead of using local repo")
	renderCmd.Flags().StringVar(&gitUser, "git-user", "", "Git username (or GIT_USER/GITHUB_USER env)")
	renderCmd.Flags().StringVar(&gitToken, "git-token", "", "Git token (or GIT_TOKEN/GITHUB_TOKEN env)")

	// PR flags
	renderCmd.Flags().BoolVar(&createPR, "create-pr", false, "Create a pull request after push")
	renderCmd.Flags().StringVar(&prTitle, "pr-title", "", "PR title (default: auto-generated)")
	renderCmd.Flags().StringVar(&prDescription, "pr-description", "", "PR description")
	renderCmd.Flags().StringSliceVar(&prLabels, "pr-labels", nil, "PR labels (comma-separated)")
	renderCmd.Flags().StringVar(&prBase, "pr-base", "main", "Base branch for PR")

	rootCmd.AddCommand(renderCmd)
}

func runRender(cmd *cobra.Command, args []string) {
	fmt.Println(logo)

	// Get API URL from flag, environment, or default
	if apiURL == "" {
		apiURL = os.Getenv("CLAIM_API_URL")
	}
	if apiURL == "" {
		apiURL = "http://localhost:8080"
	}

	// Build render config
	config := &RenderConfig{
		APIUrl:          apiURL,
		Templates:       templateNames,
		ParamsFile:      paramsFile,
		InlineParamsRaw: inlineParams,
		OutputDir:       outputDir,
		FilenamePattern: filenamePattern,
		SingleFile:      singleFile,
		DryRun:          dryRun,
	}

	// Build git config if any git flags are set
	if gitCommit || gitPush || gitBranch != "" || gitRepoURL != "" || createPR {
		config.GitConfig = &GitConfig{
			Commit:       gitCommit || gitPush || createPR, // Push/PR implies commit
			Push:         gitPush || createPR,              // PR implies push
			CreateBranch: gitCreateBranch,
			Message:      gitMessage,
			Branch:       gitBranch,
			Remote:       gitRemote,
			RepoURL:      gitRepoURL,
			User:         gitUser,
			Token:        gitToken,
		}
	}

	// Build PR config if PR flags are set
	if createPR || prTitle != "" || prDescription != "" || len(prLabels) > 0 {
		config.PRConfig = &PRConfig{
			Create:      createPR,
			Title:       prTitle,
			Description: prDescription,
			Labels:      prLabels,
			BaseBranch:  prBase,
		}
	}

	// Determine mode
	if nonInteractive {
		config.Interactive = false
	} else if interactive {
		config.Interactive = true
	} else {
		// Auto-detect: interactive if TTY, non-interactive otherwise
		config.Interactive = isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	}

	var err error
	if config.Interactive {
		// Interactive mode - prompt for API URL confirmation
		confirmedURL, promptErr := promptAPIURL(apiURL)
		if promptErr != nil {
			fmt.Printf("Error: %v\n", promptErr)
			os.Exit(1)
		}
		config.APIUrl = confirmedURL
		fmt.Printf("\nConnecting to API: %s\n\n", config.APIUrl)

		err = runInteractive(config)
	} else {
		fmt.Printf("Connecting to API: %s\n\n", config.APIUrl)
		err = runNonInteractive(config)
	}

	if err != nil {
		fmt.Println(errorStyle.Render(err.Error()))
		os.Exit(1)
	}
}
