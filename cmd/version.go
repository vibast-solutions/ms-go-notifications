package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	Version = "dev"
	Commit  = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version and build information",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("notifications-service %s (commit: %s)\n", Version, Commit)
	},
}

// init registers the version command.
func init() {
	rootCmd.AddCommand(versionCmd)
}
