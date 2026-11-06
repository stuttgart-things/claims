package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/stuttgart-things/claims/internal/banner"
)

// Command groups of the root help.
const (
	groupClaims = "claims"
	groupAuth   = "auth"
)

var rootCmd = &cobra.Command{
	Use:   "claims",
	Short: "Order, change and delete claims via Git PR",
	Long:  `claims renders claim templates from claim-machinery-api and orders, changes and deletes claims in the fleet repository via Git pull requests.`,
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

func init() {
	rootCmd.AddGroup(
		&cobra.Group{ID: groupClaims, Title: "Claims:"},
		&cobra.Group{ID: groupAuth, Title: "Auth:"},
	)

	// The banner belongs to the root help only (bare `claims`, `claims
	// --help`, `claims help`), not to subcommands or their help.
	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == rootCmd {
			banner.Show()
		}
		defaultHelp(cmd, args)
	})
}

func Execute() {
	banner.SetVersionLine(currentBuildInfo().String())
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
