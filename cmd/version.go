package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
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
		fmt.Println(logo)
		fmt.Printf("Version:    %s\n", version)
		fmt.Printf("Commit:     %s\n", commit)
		fmt.Printf("Build Date: %s\n", buildDate)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
