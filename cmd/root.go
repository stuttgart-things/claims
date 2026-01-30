package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "claims",
	Short: "Claims CLI tool",
	Long:  `Claims is a CLI tool for managing claims.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(logo)
		_ = cmd.Usage()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
