package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/stuttgart-things/claims/internal/banner"
)

var rootCmd = &cobra.Command{
	Use:   "claims",
	Short: "Claims CLI tool",
	Long:  `Claims is a CLI tool for managing claims.`,
	Run: func(cmd *cobra.Command, args []string) {
		banner.Show()
		_ = cmd.Usage()
	},
}

func Execute() {
	banner.SetVersionInfo(version, commit, buildDate)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
