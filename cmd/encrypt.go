package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	encryptAPIURL       string
	encryptTemplate     string
	encryptSecretName   string
	encryptNamespace    string
	encryptParamsFile   string
	encryptInlineParams []string
	encryptOutputDir    string
	encryptFilenamePat  string
	encryptDryRun       bool

	// Git flags for encrypt
	encryptGitBranch       string
	encryptGitCreateBranch bool
	encryptGitMessage      string
	encryptGitRemote       string
	encryptGitRepoURL      string
	encryptGitUser         string
	encryptGitToken        string

	// PR flags for encrypt
	encryptCreatePR      bool
	encryptPRTitle       string
	encryptPRDescription string
	encryptPRLabels      []string
	encryptPRBase        string

	// Mode flags for encrypt
	encryptInteractive    bool
	encryptNonInteractive bool
)

var encryptCmd = &cobra.Command{
	Use:     "encrypt",
	GroupID: groupClaims,
	Short:   "Create a SOPS-encrypted Kubernetes Secret via Git PR",
	Long:    `Fetches a template from the claim-machinery API, collects secret values, generates a Kubernetes Secret YAML, encrypts it with SOPS (age), and optionally commits via Git PR.`,
	Run:     runEncrypt,
}

func init() {
	encryptCmd.Flags().StringVarP(&encryptAPIURL, "api-url", "a", "", "API URL (default: $CLAIM_API_URL, --endpoint, the current endpoint; no localhost default)")
	addEndpointFlag(encryptCmd)
	addPreflightFlag(encryptCmd)
	encryptCmd.Flags().StringVarP(&encryptTemplate, "template", "t", "", "Template name to use")
	encryptCmd.Flags().StringVar(&encryptSecretName, "name", "", "Secret name")
	encryptCmd.Flags().StringVar(&encryptNamespace, "namespace", "", "Secret namespace")
	encryptCmd.Flags().StringVarP(&encryptParamsFile, "params-file", "f", "", "YAML/JSON file with parameters")
	encryptCmd.Flags().StringSliceVarP(&encryptInlineParams, "param", "p", nil, "Inline param (key=value, repeatable)")
	encryptCmd.Flags().StringVarP(&encryptOutputDir, "output-dir", "o", ".", "Output directory for encrypted file")
	encryptCmd.Flags().StringVar(&encryptFilenamePat, "filename-pattern", "{{.name}}-secret.enc.yaml", "Pattern for output filename")
	encryptCmd.Flags().BoolVar(&encryptDryRun, "dry-run", false, "Show encrypted output without writing files")

	// Git flags
	encryptCmd.Flags().StringVar(&encryptGitBranch, "git-branch", "", "Branch to use/create")
	encryptCmd.Flags().BoolVar(&encryptGitCreateBranch, "git-create-branch", false, "Create the branch if it doesn't exist")
	encryptCmd.Flags().StringVar(&encryptGitMessage, "git-message", "", "Commit message (default: auto-generated)")
	encryptCmd.Flags().StringVar(&encryptGitRemote, "git-remote", "origin", "Git remote name")
	encryptCmd.Flags().StringVar(&encryptGitRepoURL, "git-repo-url", "", "Clone from URL instead of using local repo")
	encryptCmd.Flags().StringVar(&encryptGitUser, "git-user", "", "Git username (or GIT_USER/GITHUB_USER env)")
	encryptCmd.Flags().StringVar(&encryptGitToken, "git-token", "", "Git token (or GIT_TOKEN/GITHUB_TOKEN env)")

	// PR flags
	encryptCmd.Flags().BoolVar(&encryptCreatePR, "create-pr", false, "Create a pull request after push")
	encryptCmd.Flags().StringVar(&encryptPRTitle, "pr-title", "", "PR title (default: auto-generated)")
	encryptCmd.Flags().StringVar(&encryptPRDescription, "pr-description", "", "PR description")
	encryptCmd.Flags().StringSliceVar(&encryptPRLabels, "pr-labels", nil, "PR labels (comma-separated)")
	encryptCmd.Flags().StringVar(&encryptPRBase, "pr-base", "main", "Base branch for PR")

	// Mode flags
	encryptCmd.Flags().BoolVarP(&encryptInteractive, "interactive", "i", false, "Force interactive mode")
	encryptCmd.Flags().BoolVar(&encryptNonInteractive, "non-interactive", false, "Force non-interactive mode")

	addAPIAuthFlags(encryptCmd)

	rootCmd.AddCommand(encryptCmd)
}

func runEncrypt(cmd *cobra.Command, args []string) {

	config := &EncryptConfig{
		Template:        encryptTemplate,
		SecretName:      encryptSecretName,
		SecretNamespace: encryptNamespace,
		ParamsFile:      encryptParamsFile,
		InlineParamsRaw: encryptInlineParams,
		OutputDir:       encryptOutputDir,
		FilenamePattern: encryptFilenamePat,
		DryRun:          encryptDryRun,
	}

	// Build git config if any git flags are set
	if encryptGitBranch != "" || encryptGitRepoURL != "" || encryptCreatePR {
		config.GitConfig = &GitConfig{
			Commit:       true,
			Push:         true,
			CreateBranch: encryptGitCreateBranch,
			Message:      encryptGitMessage,
			Branch:       encryptGitBranch,
			Remote:       encryptGitRemote,
			RepoURL:      encryptGitRepoURL,
			User:         encryptGitUser,
			Token:        encryptGitToken,
		}
	}

	// Build PR config if PR flags are set
	if encryptCreatePR || encryptPRTitle != "" || encryptPRDescription != "" || len(encryptPRLabels) > 0 {
		config.PRConfig = &PRConfig{
			Create:      encryptCreatePR,
			Title:       encryptPRTitle,
			Description: encryptPRDescription,
			Labels:      encryptPRLabels,
			BaseBranch:  encryptPRBase,
		}
	}

	// Determine mode
	if encryptNonInteractive {
		config.Interactive = false
	} else if encryptInteractive {
		config.Interactive = true
	} else {
		config.Interactive = isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	}

	auth, authErr := resolveAPIAuth()
	if authErr != nil {
		fmt.Println(errorStyle.Render(authErr.Error()))
		os.Exit(1)
	}
	config.Auth = auth

	// API: -a, $CLAIM_API_URL, --endpoint/$CLAIM_ENDPOINT, the current
	// endpoint; no localhost default (claims#116). Then the preflight
	// (claims#114) before anything is written.
	target, targetErr := resolveAPITarget(encryptAPIURL, config.Interactive)
	if isAbort(targetErr) || (targetErr != nil && skipPreflight) {
		exitOn(targetErr)
	}
	report := &preflightReport{}
	if checkAPITarget(report, target, targetErr) {
		config.APIUrl = splitAPIURLs(target.URL)[0]
		_, _ = fmt.Fprintf(os.Stdout, "API: %s\n", targetLabel(target, config.APIUrl))
		if !skipPreflight {
			checkAPI(context.Background(), report, target, config.APIUrl, config.Auth)
		}
	}
	if config.GitConfig != nil && !config.DryRun && !skipPreflight {
		checkGit(context.Background(), report, gitPlanFor(config.GitConfig, config.PRConfig, config.OutputDir))
	}
	exitOn(finishPreflight(report))

	var err error
	if config.Interactive {
		err = runEncryptInteractive(config)
	} else {
		err = runEncryptNonInteractive(config)
	}

	exitOn(err)
}
