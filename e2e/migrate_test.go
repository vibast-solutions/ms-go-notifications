//go:build e2e

// Package e2e holds integration tests that run against the docker-compose
// MySQL (make e2e). They are gated behind the e2e build tag so they never run
// with a plain `go test ./...`.
package e2e

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/migrations"
)

const defaultDSN = "root:testpass@tcp(127.0.0.1:13306)/notifications_test?parseTime=true&multiStatements=true"

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("E2E_DATABASE_DSN")
	if dsn == "" {
		dsn = defaultDSN
	}
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// The container may still be warming up.
	deadline := time.Now().Add(60 * time.Second)
	for {
		if err = db.Ping(); err == nil {
			return db
		}
		if time.Now().After(deadline) {
			t.Fatalf("mysql not reachable: %v", err)
		}
		time.Sleep(2 * time.Second)
	}
}

func TestMigrateCreatesAllTables(t *testing.T) {
	db := openDB(t)

	require.NoError(t, migrations.Run(db))

	rows, err := db.Query("SHOW TABLES")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	tables := map[string]bool{}
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		tables[name] = true
	}
	require.NoError(t, rows.Err())

	for _, want := range []string{
		"api_keys",
		"api_key_domains",
		"messages",
		"message_recipients",
		"message_attachments",
		"message_events",
		"suppressions",
		"settings",
		"schema_migrations", // golang-migrate's version table
	} {
		assert.Truef(t, tables[want], "table %s missing after migrate", want)
	}

	// Running again must be a no-op, not an error.
	require.NoError(t, migrations.Run(db))
}
