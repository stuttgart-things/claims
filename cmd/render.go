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
	fullPreview     bool
	toStdout        bool
	singleFile      bool
	filenamePattern string
	templateNames   []string

	// Non-interactive mode flags
	paramsFile     string
	inlineParams   []string
	inlineSecrets  []string
	skipSecrets    bool
	combineSecrets bool
	interactive    bool
	nonInteractive bool
	fileMode       string
	layoutName     string

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
	prMerge       bool
)

var renderCmd = &cobra.Command{
	Use:     "render",
	GroupID: groupClaims,
	Short:   "Render claim templates, save locally or via Git PR",
	Long:    `Connects to the claim-machinery API, fetches available templates, and provides an interactive form to render claims.`,
	Run:     runRender,
}

func init() {
	renderCmd.Flags().StringVarP(&apiURL, "api-url", "a", "", "API URL (default: $CLAIM_API_URL or http://localhost:8080)")
	renderCmd.Flags().StringVarP(&outputDir, "output-dir", "o", ".", "Output directory for rendered files")
	renderCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print output without writing files")
	renderCmd.Flags().BoolVar(&fullPreview, "full", false, "Interactive review: show the complete YAML instead of the first lines")
	renderCmd.Flags().BoolVar(&toStdout, "stdout", false, "Print the rendered YAML to stdout and write nothing (non-interactive; progress goes to stderr)")
	renderCmd.Flags().BoolVar(&singleFile, "single-file", false, "Combine all resources into one file")
	renderCmd.Flags().StringVar(&filenamePattern, "filename-pattern", "{{.template}}-{{.name}}.yaml", "Pattern for output filenames")
	renderCmd.Flags().StringSliceVarP(&templateNames, "templates", "t", nil, "Templates to render (comma-separated or repeated)")

	// Non-interactive mode flags
	renderCmd.Flags().StringVarP(&paramsFile, "params-file", "f", "", "YAML/JSON file with parameters")
	renderCmd.Flags().StringArrayVarP(&inlineParams, "param", "p", nil, "Inline param (key=value, repeatable)")
	renderCmd.Flags().StringArrayVarP(&inlineSecrets, "secret", "s", nil, "Secret param (key=value, repeatable)")
	renderCmd.Flags().BoolVar(&skipSecrets, "skip-secrets", false, "Skip secret generation even if template defines them")
	renderCmd.Flags().BoolVar(&combineSecrets, "combine-secrets", false, "Save encrypted secrets in the same file as rendered output (--- separated)")
	renderCmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "Force interactive mode")
	renderCmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Force non-interactive mode")
	renderCmd.Flags().StringVar(&fileMode, "file-mode", "overwrite", "File write mode: overwrite (default) or append")
	renderCmd.Flags().StringVar(&layoutName, "layout", "", "Repository layout: source-path writes every document to the path its stuttgart-things.com/source-path or target-path annotation names, relative to --output-dir as the repository root; appends to a kustomization.yaml next to it; no registry; PR conventions of the Backstage templates")

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
	renderCmd.Flags().BoolVar(&prMerge, "merge", false, "With --create-pr: wait for the PR's checks, then squash-merge it (see claims merge)")

	addAPIAuthFlags(renderCmd)

	rootCmd.AddCommand(renderCmd)
}

// validateStdout rejects flags that do not go with --stdout, which only
// prints the rendered YAML.
func validateStdout(config *RenderConfig, forceInteractive bool) error {
	switch {
	case forceInteractive:
		return fmt.Errorf("--stdout is non-interactive; drop --interactive")
	case config.GitConfig != nil || config.PRConfig != nil:
		return fmt.Errorf("--stdout writes nothing; it does not combine with --git-* or --create-pr/--pr-* flags")
	case config.Layout != "":
		return fmt.Errorf("--stdout writes nothing; it does not combine with --layout")
	case config.DryRun:
		return fmt.Errorf("--stdout already writes nothing; drop --dry-run")
	}
	return nil
}

func runRender(cmd *cobra.Command, args []string) {

	// Get API URL from flag, environment, or default.
	// CLAIM_API_URL supports colon-separated multiple endpoints (URL colons preserved).
	if apiURL == "" {
		apiURL = os.Getenv("CLAIM_API_URL")
	}
	if apiURL == "" {
		apiURL = "http://localhost:8080"
	}

	// Build render config
	config := &RenderConfig{
		APIUrl:           apiURL,
		APIUrls:          splitAPIURLs(apiURL),
		Templates:        templateNames,
		ParamsFile:       paramsFile,
		InlineParamsRaw:  inlineParams,
		InlineSecretsRaw: inlineSecrets,
		SkipSecrets:      skipSecrets,
		CombineSecrets:   combineSecrets,
		OutputDir:        outputDir,
		FilenamePattern:  filenamePattern,
		SingleFile:       singleFile,
		DryRun:           dryRun,
		FullPreview:      fullPreview,
		Stdout:           toStdout,
		FileMode:         fileMode,
		Layout:           layoutName,
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

	if prMerge && !createPR {
		exitOn(fmt.Errorf("--merge merges the PR --create-pr opens: add --create-pr"))
	}

	// Build PR config if PR flags are set
	if createPR || prTitle != "" || prDescription != "" || len(prLabels) > 0 {
		config.PRConfig = &PRConfig{
			Create:      createPR,
			Title:       prTitle,
			Description: prDescription,
			Labels:      prLabels,
			BaseBranch:  prBase,
			Merge:       prMerge,
		}
	}

	// Determine mode
	if config.Stdout {
		if err := validateStdout(config, interactive); err != nil {
			exitOn(err)
		}
		config.Interactive = false
	} else if nonInteractive {
		config.Interactive = false
	} else if interactive {
		config.Interactive = true
	} else {
		// Auto-detect: interactive if TTY, non-interactive otherwise
		config.Interactive = isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	}

	if err := validateLayout(config); err != nil {
		fmt.Println(errorStyle.Render(err.Error()))
		os.Exit(1)
	}

	auth, authErr := resolveAPIAuth()
	if authErr != nil {
		fmt.Println(errorStyle.Render(authErr.Error()))
		os.Exit(1)
	}
	config.Auth = auth

	var err error
	if config.Interactive {
		// Interactive mode — select or confirm API endpoint
		var selectedURL string
		var promptErr error
		if len(config.APIUrls) > 1 {
			selectedURL, promptErr = selectAPIEndpoint(config.APIUrls)
		} else {
			selectedURL, promptErr = promptAPIURL(config.APIUrls[0])
		}
		exitOn(promptErr)
		config.APIUrl = selectedURL
		fmt.Printf("\nConnecting to API: %s\n", config.APIUrl)

		err = runInteractive(config)
	} else {
		// Non-interactive: use first URL
		config.APIUrl = config.APIUrls[0]
		_, _ = fmt.Fprintf(config.progressOut(), "Connecting to API: %s\n\n", config.APIUrl)
		err = runNonInteractive(config)
	}

	exitOn(err)
}
