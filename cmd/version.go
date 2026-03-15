package cmd

import (
	"github.com/spf13/cobra"
	"github.com/stuttgart-things/claims/internal/banner"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Print the version, commit SHA, and build date of the claims CLI.`,
	Run: func(cmd *cobra.Command, args []string) {
		banner.Show()
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
