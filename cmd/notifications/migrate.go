package main

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/vibast-solutions/ms-go-notifications/config"
	"github.com/vibast-solutions/ms-go-notifications/migrations"
)

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending database migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrate()
		},
	}
}

func runMigrate() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	configureLogging(cfg)

	db, err := sql.Open("mysql", withMultiStatements(cfg.DatabaseDSN))
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := migrations.Run(db); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	log.Info("migrations applied")
	return nil
}

// withMultiStatements ensures the DSN allows multi-statement execution, which
// golang-migrate needs because each migration file holds several statements.
func withMultiStatements(dsn string) string {
	if strings.Contains(dsn, "multiStatements=") {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "multiStatements=true"
}
