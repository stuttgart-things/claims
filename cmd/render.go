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
)

var renderCmd = &cobra.Command{
	Use:   "render",
	Short: "Render a claim template interactively",
	Long:  `Connects to the claim-machinery API, fetches available templates, and provides an interactive form to render claims.`,
	Run:   runRender,
}

func init() {
	renderCmd.Flags().StringVarP(&apiURL, "api-url", "a", "", "API URL (default: $CLAIM_API_URL or http://localhost:8080)")
	renderCmd.Flags().StringVarP(&outputDir, "output-dir", "o", "/tmp", "Output directory for rendered files")
	renderCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print output without writing files")
	renderCmd.Flags().BoolVar(&singleFile, "single-file", false, "Combine all resources into one file")
	renderCmd.Flags().StringVar(&filenamePattern, "filename-pattern", "{{.template}}-{{.name}}.yaml", "Pattern for output filenames")
	renderCmd.Flags().StringSliceVarP(&templateNames, "templates", "t", nil, "Templates to render (comma-separated or repeated)")

	// Non-interactive mode flags
	renderCmd.Flags().StringVarP(&paramsFile, "params-file", "f", "", "YAML/JSON file with parameters")
	renderCmd.Flags().StringSliceVarP(&inlineParams, "param", "p", nil, "Inline param (key=value, repeatable)")
	renderCmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "Force interactive mode")
	renderCmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Force non-interactive mode")

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
