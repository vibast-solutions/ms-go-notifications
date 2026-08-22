// Package migrations embeds the SQL migration files and applies them with
// golang-migrate. The `notifications migrate` command and the e2e tests share
// Run. golang-migrate manages its own schema_migrations version table.
package migrations

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed *.sql
var files embed.FS

// Run applies all pending migrations. The db connection must allow
// multi-statement execution (DSN param multiStatements=true). Already
// up-to-date is not an error.
func Run(db *sql.DB) error {
	src, err := iofs.New(files, ".")
	if err != nil {
		return fmt.Errorf("reading embedded migrations: %w", err)
	}
	driver, err := migratemysql.WithInstance(db, &migratemysql.Config{})
	if err != nil {
		return fmt.Errorf("preparing mysql driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "mysql", driver)
	if err != nil {
		return fmt.Errorf("initializing migrate: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}
