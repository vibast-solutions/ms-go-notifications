package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "notifications",
	Short: "Notifications microservice",
	Long:  "A notifications microservice providing delivery of email/SMS/push notifications via HTTP and gRPC.",
}

// Execute runs the root Cobra command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
