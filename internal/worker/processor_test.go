package worker

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/pkg/provider"
)

var procNow = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// fakeProvider is an EmailProvider returning a fixed id/error and recording the
// outbound message.
type fakeProvider struct {
	id    string
	err   error
	calls int
	last  *provider.OutboundEmail
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Send(_ context.Context, msg *provider.OutboundEmail) (string, error) {
	f.calls++
	f.last = msg
	return f.id, f.err
}

// captureEnqueuer records enqueued ids.
type captureEnqueuer struct{ ids []string }

func (c *captureEnqueuer) Enqueue(id string) bool { c.ids = append(c.ids, id); return true }

// procHarness wires a Processor over a real repository backed by sqlmock plus a
// fake provider, with deterministic clock/jitter and a captured retry timer.
type procHarness struct {
	p        *Processor
	mock     sqlmock.Sqlmock
	prov     *fakeProvider
	enqueuer *captureEnqueuer

	backoff time.Duration
	timerFn func()
}

func newProcHarness(t *testing.T, prov *fakeProvider, cfg ProcessorConfig) *procHarness {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	h := &procHarness{mock: mock, prov: prov, enqueuer: &captureEnqueuer{}}
	p := NewProcessor(repository.NewMessageRepository(db), repository.NewEventRepository(db),
		prov, h.enqueuer, cfg, nil)
	p.clock = func() time.Time { return procNow }
	p.jitter = func(d time.Duration) time.Duration { return d } // identity for exact bounds
	p.afterFunc = func(d time.Duration, f func()) *time.Timer {
		h.backoff = d
		h.timerFn = f
		return nil
	}
	h.p = p
	return h
}

func messageRow(attempts int) *sqlmock.Rows {
	cols := []string{
		"id", "api_key_id", "channel", "status", "provider", "provider_message_id",
		"from_email", "from_name", "from_domain", "reply_to", "subject", "text_body", "html_body",
		"headers", "tags", "metadata", "idempotency_key", "attempts", "last_error",
		"next_attempt_at", "locked_at", "locked_by", "recipient_count",
		"created_at", "updated_at", "sent_at", "finished_at",
	}
	return sqlmock.NewRows(cols).AddRow(
		"01MSG", "01KEY", "email", "sending", nil, nil,
		"no-reply@vibast.ro", nil, "vibast.ro", nil, "Hi", []byte("hello"), nil,
		nil, nil, nil, nil, attempts, nil,
		nil, procNow, "host:1", 1,
		procNow, procNow, nil, nil,
	)
}

// expectClaimWon sets up a winning claim.
func (h *procHarness) expectClaimWon() {
	h.mock.ExpectExec("status='sending'").
		WithArgs(procNow, h.p.lockedBy, procNow, "01MSG", procNow).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

// expectLoad sets up the message + recipients + attachments loads for a claimed
// message with the given attempt count and one pending 'to' recipient.
func (h *procHarness) expectLoad(attempts int) {
	h.mock.ExpectQuery("SELECT .+ FROM messages WHERE id = ?").
		WithArgs("01MSG").WillReturnRows(messageRow(attempts))
	h.mock.ExpectQuery("SELECT .+ FROM message_recipients WHERE message_id = ?").
		WithArgs("01MSG").
		WillReturnRows(sqlmock.NewRows([]string{"id", "message_id", "type", "email", "name", "status",
			"bounce_type", "bounce_subtype", "diagnostic", "last_event_at"}).
			AddRow("01R", "01MSG", "to", "jane@example.com", nil, "pending", nil, nil, nil, nil))
	h.mock.ExpectQuery("SELECT .+ FROM message_attachments WHERE message_id = ?").
		WithArgs("01MSG").
		WillReturnRows(sqlmock.NewRows([]string{"id", "message_id", "filename", "content_type", "size_bytes", "content", "purged_at"}))
}

func TestProcessClaimLostNoSend(t *testing.T) {
	prov := &fakeProvider{id: "x"}
	h := newProcHarness(t, prov, ProcessorConfig{MaxAttempts: 5, BackoffBase: 5 * time.Second})

	h.mock.ExpectExec("status='sending'").
		WithArgs(procNow, h.p.lockedBy, procNow, "01MSG", procNow).
		WillReturnResult(sqlmock.NewResult(0, 0)) // zero rows → not ours

	h.p.Process(context.Background(), "01MSG")

	assert.Equal(t, 0, prov.calls, "must not send when the claim loses")
	assert.NoError(t, h.mock.ExpectationsWereMet())
}

func TestProcessSuccess(t *testing.T) {
	prov := &fakeProvider{id: "prov-123"}
	h := newProcHarness(t, prov, ProcessorConfig{MaxAttempts: 5, BackoffBase: 5 * time.Second})

	h.expectClaimWon()
	h.expectLoad(1)
	h.mock.ExpectExec("status='sent'").
		WithArgs("fake", "prov-123", procNow, procNow, procNow, "01MSG").
		WillReturnResult(sqlmock.NewResult(0, 1))
	h.mock.ExpectExec(regexp.QuoteMeta("UPDATE message_recipients SET status=? WHERE message_id=? AND status=?")).
		WithArgs("sent", "01MSG", "pending").
		WillReturnResult(sqlmock.NewResult(0, 1))
	h.mock.ExpectExec(regexp.QuoteMeta("INSERT INTO message_events")).
		WithArgs(sqlmock.AnyArg(), "01MSG", nil, "attempt", nil, sqlmock.AnyArg(), procNow, procNow).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h.p.Process(context.Background(), "01MSG")

	assert.Equal(t, 1, prov.calls)
	require.NotNil(t, prov.last)
	assert.Equal(t, "no-reply@vibast.ro", prov.last.From)
	assert.Equal(t, []string{"jane@example.com"}, prov.last.To)
	assert.Contains(t, string(prov.last.Raw), "Subject: Hi")
	assert.NoError(t, h.mock.ExpectationsWereMet())
}

func TestProcessRetryableSchedulesRetry(t *testing.T) {
	prov := &fakeProvider{err: &provider.ProviderError{Retryable: true, Code: "Throttling", Err: errors.New("slow down")}}
	h := newProcHarness(t, prov, ProcessorConfig{MaxAttempts: 5, BackoffBase: 5 * time.Second})

	h.expectClaimWon()
	h.expectLoad(1) // attempt 1 of 5 → retry
	next := procNow.Add(5 * time.Second)
	h.mock.ExpectExec("status='queued'").
		WithArgs("fake", sqlmock.AnyArg(), next, procNow, "01MSG").
		WillReturnResult(sqlmock.NewResult(0, 1))
	h.mock.ExpectExec(regexp.QuoteMeta("INSERT INTO message_events")).
		WithArgs(sqlmock.AnyArg(), "01MSG", nil, "attempt", nil, sqlmock.AnyArg(), procNow, procNow).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h.p.Process(context.Background(), "01MSG")

	// Backoff is base×2^0 = 5s (jitter is identity here), and the timer re-arms
	// the fast path.
	assert.Equal(t, 5*time.Second, h.backoff)
	require.NotNil(t, h.timerFn)
	h.timerFn()
	assert.Equal(t, []string{"01MSG"}, h.enqueuer.ids)
	assert.NoError(t, h.mock.ExpectationsWereMet())
}

func TestProcessRetryBackoffGrowsWithAttempts(t *testing.T) {
	prov := &fakeProvider{err: &provider.ProviderError{Retryable: true, Err: errors.New("x")}}
	h := newProcHarness(t, prov, ProcessorConfig{MaxAttempts: 5, BackoffBase: 5 * time.Second})

	h.expectClaimWon()
	h.expectLoad(3) // attempt 3 → base×2^2 = 20s
	next := procNow.Add(20 * time.Second)
	h.mock.ExpectExec("status='queued'").
		WithArgs("fake", sqlmock.AnyArg(), next, procNow, "01MSG").
		WillReturnResult(sqlmock.NewResult(0, 1))
	h.mock.ExpectExec(regexp.QuoteMeta("INSERT INTO message_events")).
		WithArgs(sqlmock.AnyArg(), "01MSG", nil, "attempt", nil, sqlmock.AnyArg(), procNow, procNow).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h.p.Process(context.Background(), "01MSG")
	assert.Equal(t, 20*time.Second, h.backoff)
	assert.NoError(t, h.mock.ExpectationsWereMet())
}

func TestProcessPermanentFails(t *testing.T) {
	prov := &fakeProvider{err: &provider.ProviderError{Retryable: false, Code: "MessageRejected", Err: errors.New("nope")}}
	h := newProcHarness(t, prov, ProcessorConfig{MaxAttempts: 5, BackoffBase: 5 * time.Second})

	h.expectClaimWon()
	h.expectLoad(1)
	h.mock.ExpectExec("status='failed'").
		WithArgs("fake", sqlmock.AnyArg(), procNow, procNow, "01MSG").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Two events: attempt{failed} then failed.
	h.mock.ExpectExec(regexp.QuoteMeta("INSERT INTO message_events")).
		WithArgs(sqlmock.AnyArg(), "01MSG", nil, "attempt", nil, sqlmock.AnyArg(), procNow, procNow).
		WillReturnResult(sqlmock.NewResult(0, 1))
	h.mock.ExpectExec(regexp.QuoteMeta("INSERT INTO message_events")).
		WithArgs(sqlmock.AnyArg(), "01MSG", nil, "failed", nil, sqlmock.AnyArg(), procNow, procNow).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h.p.Process(context.Background(), "01MSG")

	assert.Nil(t, h.timerFn, "permanent failure must not arm a retry timer")
	assert.NoError(t, h.mock.ExpectationsWereMet())
}

func TestProcessMaxAttemptsFails(t *testing.T) {
	// A retryable error on the final attempt exhausts the budget → failed.
	prov := &fakeProvider{err: &provider.ProviderError{Retryable: true, Err: errors.New("still failing")}}
	h := newProcHarness(t, prov, ProcessorConfig{MaxAttempts: 5, BackoffBase: 5 * time.Second})

	h.expectClaimWon()
	h.expectLoad(5) // attempts==maxAttempts → no more retries
	h.mock.ExpectExec("status='failed'").
		WithArgs("fake", sqlmock.AnyArg(), procNow, procNow, "01MSG").
		WillReturnResult(sqlmock.NewResult(0, 1))
	h.mock.ExpectExec(regexp.QuoteMeta("INSERT INTO message_events")).
		WithArgs(sqlmock.AnyArg(), "01MSG", nil, "attempt", nil, sqlmock.AnyArg(), procNow, procNow).
		WillReturnResult(sqlmock.NewResult(0, 1))
	h.mock.ExpectExec(regexp.QuoteMeta("INSERT INTO message_events")).
		WithArgs(sqlmock.AnyArg(), "01MSG", nil, "failed", nil, sqlmock.AnyArg(), procNow, procNow).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h.p.Process(context.Background(), "01MSG")
	assert.Nil(t, h.timerFn, "exhausted attempts must not arm a retry timer")
	assert.NoError(t, h.mock.ExpectationsWereMet())
}

func TestDefaultJitterWithinBounds(t *testing.T) {
	base := 100 * time.Second
	for i := 0; i < 1000; i++ {
		got := defaultJitter(base)
		assert.GreaterOrEqual(t, got, time.Duration(float64(base)*0.8))
		assert.LessOrEqual(t, got, time.Duration(float64(base)*1.2))
	}
}
