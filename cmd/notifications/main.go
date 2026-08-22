// Command notifications is the Vibast notifications microservice binary.
// Subcommands: serve (HTTP API + in-process workers), migrate (apply DB
// migrations). apikey and cleanup arrive in later stories.
package main

import (
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/vibast-solutions/ms-go-notifications/config"
)

func main() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetLevel(log.InfoLevel)

	rootCmd := &cobra.Command{
		Use:           "notifications",
		Short:         "Vibast notifications microservice",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	rootCmd.AddCommand(newServeCmd(), newMigrateCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// configureLogging applies LOG_LEVEL and LOG_FORMAT from config to logrus.
func configureLogging(cfg *config.Config) {
	if lvl, err := log.ParseLevel(cfg.LogLevel); err == nil {
		log.SetLevel(lvl)
	} else {
		log.WithField("level", cfg.LogLevel).Warn("unknown LOG_LEVEL, keeping info")
	}
	if cfg.LogFormat == "text" {
		log.SetFormatter(&log.TextFormatter{})
	} else {
		log.SetFormatter(&log.JSONFormatter{})
	}
}
