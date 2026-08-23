//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	logrus "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
	"github.com/vibast-solutions/ms-go-notifications/internal/worker"
	"github.com/vibast-solutions/ms-go-notifications/migrations"
	providerlog "github.com/vibast-solutions/ms-go-notifications/pkg/provider/log"
)

// safeBuffer is a concurrency-safe buffer for capturing logrus output written by
// the worker goroutines.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// procDeps holds the wired send machinery for an e2e run.
type procDeps struct {
	svc        *service.EmailService
	dispatcher *worker.Dispatcher
	sweeper    *worker.Sweeper
}

// buildProcessing wires a full in-process send pipeline (repositories, log
// provider, worker pool, sweeper) against db. When dispatchToPool is true the
// accept path hands committed ids to the real pool (fast path); when false it
// uses a NoopDispatcher, so the row is left queued for the sweeper to find —
// modelling a crash right after accept. The caller starts dispatcher/sweeper.
func buildProcessing(t *testing.T, db *sql.DB, dispatchToPool bool) *procDeps {
	t.Helper()
	messages := repository.NewMessageRepository(db)
	events := repository.NewEventRepository(db)
	settings := repository.NewSettingsRepository(db)

	pool := worker.NewDispatcher(worker.DispatcherConfig{
		Concurrency: 2, BufferSize: 64,
	}, settings, logrus.StandardLogger())
	processor := worker.NewProcessor(messages, events, providerlog.New(), pool,
		worker.ProcessorConfig{MaxAttempts: 5, BackoffBase: time.Second}, logrus.StandardLogger())
	pool.SetProcessor(processor)
	sweeper := worker.NewSweeper(messages, pool, worker.SweeperConfig{
		Interval: 200 * time.Millisecond, StaleLockAfter: time.Minute,
	}, logrus.StandardLogger())

	var dispatcher service.Dispatcher = worker.NoopDispatcher{}
	if dispatchToPool {
		dispatcher = pool
	}
	svc := service.NewEmailService(messages, repository.NewSuppressionRepository(db), dispatcher,
		dtov1.Limits{MaxRecipients: 50, MaxPayloadBytes: 10 << 20})

	return &procDeps{svc: svc, dispatcher: pool, sweeper: sweeper}
}

func truncateAll(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"messages", "message_recipients", "message_events", "message_attachments", "suppressions", "settings"} {
		_, err := db.Exec("DELETE FROM " + table)
		require.NoError(t, err)
	}
}

func sampleRequest() *dtov1.SendEmailRequest {
	return &dtov1.SendEmailRequest{
		From:    dtov1.Address{Email: "no-reply@example.com", Name: "Example"},
		To:      []dtov1.Address{{Email: "jane@recipient.test"}},
		Subject: "Hello from e2e",
		Text:    "plain body",
		HTML:    "<p>rich body</p>",
	}
}

func sendKey() *entity.APIKey {
	return &entity.APIKey{ID: entity.NewID(), Scopes: []string{"send"}, Domains: []string{"example.com"}}
}

// pollStatus waits until the message reaches want or the timeout elapses.
func pollStatus(t *testing.T, db *sql.DB, id, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var status string
	for time.Now().Before(deadline) {
		require.NoError(t, db.QueryRow("SELECT status FROM messages WHERE id = ?", id).Scan(&status))
		if status == want {
			return status
		}
		time.Sleep(50 * time.Millisecond)
	}
	return status
}

func TestEndToEndAcceptThenSent(t *testing.T) {
	db := openDB(t)
	require.NoError(t, migrations.Run(db))
	truncateAll(t, db)

	logs := &safeBuffer{}
	logrus.SetOutput(logs)
	t.Cleanup(func() { logrus.SetOutput(os.Stderr) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The dispatcher is wired into the accept path so a POST is dispatched
	// immediately (the sweeper is the safety net).
	deps := buildProcessing(t, db, true)
	deps.dispatcher.Start(ctx)
	go deps.sweeper.Run(ctx)
	defer deps.dispatcher.Stop(5 * time.Second)

	msg, created, err := deps.svc.Accept(ctx, sendKey(), sampleRequest())
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, entity.MessageStatusQueued, msg.Status)

	status := pollStatus(t, db, msg.ID, entity.MessageStatusSent, 5*time.Second)
	assert.Equal(t, entity.MessageStatusSent, status, "message must be sent within 5s")

	// Provider id recorded, recipients sent, an attempt event stored, log line printed.
	var providerName, providerMessageID sql.NullString
	require.NoError(t, db.QueryRow("SELECT provider, provider_message_id FROM messages WHERE id = ?", msg.ID).
		Scan(&providerName, &providerMessageID))
	assert.Equal(t, "log", providerName.String)
	assert.True(t, strings.HasPrefix(providerMessageID.String, "log-"))

	var recipientStatus string
	require.NoError(t, db.QueryRow("SELECT status FROM message_recipients WHERE message_id = ?", msg.ID).Scan(&recipientStatus))
	assert.Equal(t, entity.RecipientStatusSent, recipientStatus)

	var attemptEvents int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM message_events WHERE message_id = ? AND type = 'attempt'", msg.ID).Scan(&attemptEvents))
	assert.GreaterOrEqual(t, attemptEvents, 1)

	assert.Contains(t, logs.String(), "email sent (log provider)", "log provider prints a send line")
}

func TestEndToEndSweeperDrainsQueuedAfterRestart(t *testing.T) {
	db := openDB(t)
	require.NoError(t, migrations.Run(db))
	truncateAll(t, db)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// "Before restart": accept with a NoopDispatcher so the row is left queued
	// and never dispatched in-process (simulating a crash right after accept).
	accepted := buildProcessing(t, db, false)
	msg, created, err := accepted.svc.Accept(ctx, sendKey(), sampleRequest())
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, entity.MessageStatusQueued, msg.Status)

	// It is still queued — nothing processed it yet.
	status := pollStatus(t, db, msg.ID, entity.MessageStatusSent, 300*time.Millisecond)
	require.Equal(t, entity.MessageStatusQueued, status, "no worker ran yet")

	// "After restart": a fresh worker set starts; the sweeper's startup pass
	// discovers the queued row and drains it.
	restarted := buildProcessing(t, db, false)
	restarted.dispatcher.Start(ctx)
	go restarted.sweeper.Run(ctx)
	defer restarted.dispatcher.Stop(5 * time.Second)

	status = pollStatus(t, db, msg.ID, entity.MessageStatusSent, 5*time.Second)
	assert.Equal(t, entity.MessageStatusSent, status, "sweeper drains the queued row after restart")
}
