package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/stuttgart-things/claims/internal/templates"
)

var (
	apiURL           string
	outputDir        string
	dryRun           bool
	singleFile       bool
	filenamePattern  string
	templateNames    []string
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

	// Allow user to confirm or change API URL
	confirmedURL, err := promptAPIURL(apiURL)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	apiURL = confirmedURL

	fmt.Printf("\nConnecting to API: %s\n\n", apiURL)

	// Create API client
	client := templates.NewClient(apiURL)

	// Run interactive render flow
	runInteractiveRender(client)
}
