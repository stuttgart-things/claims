package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/stuttgart-things/claims/internal/buildinfo"
)

// Set via -ldflags -X github.com/stuttgart-things/claims/cmd.<name>=… (see
// Taskfile.yaml build-local and .goreleaser.yaml). Empty values fall back to
// the build info the Go toolchain embeds (internal/buildinfo).
var (
	version   = ""
	commit    = ""
	buildDate = ""
)

func currentBuildInfo() buildinfo.Info {
	return buildinfo.Current(version, commit, buildDate)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Print the version, commit SHA, and build date of the claims CLI. Unknown fields are left out.`,
	Run: func(cmd *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "claims "+currentBuildInfo().String())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
