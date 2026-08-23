package repository

import (
	"context"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

func newEventRepo(t *testing.T) (*EventRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewEventRepository(db), mock
}

func TestEventInsert(t *testing.T) {
	repo, mock := newEventRepo(t)
	rcpt, pid := "jane@example.com", "sns-1"
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO message_events")).
		WithArgs("01E", "01MSG", &rcpt, "delivery", &pid, nil, testTime, testTime).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.Insert(context.Background(), &entity.MessageEvent{
		ID: "01E", MessageID: "01MSG", RecipientEmail: &rcpt, Type: entity.EventTypeDelivery,
		ProviderEventID: &pid, OccurredAt: testTime, CreatedAt: testTime,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestEventInsertDuplicateMapsToErrDuplicateKey verifies the dedupe contract:
// a unique-index violation on (provider_event_id, recipient_email) surfaces as
// ErrDuplicateKey so the event service can skip an already-applied SNS delivery.
func TestEventInsertDuplicateMapsToErrDuplicateKey(t *testing.T) {
	repo, mock := newEventRepo(t)
	rcpt, pid := "jane@example.com", "sns-1"
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO message_events")).
		WillReturnError(&mysql.MySQLError{Number: mysqlErrDupEntry, Message: "duplicate"})

	err := repo.Insert(context.Background(), &entity.MessageEvent{
		ID: "01E", MessageID: "01MSG", RecipientEmail: &rcpt, Type: entity.EventTypeDelivery,
		ProviderEventID: &pid, OccurredAt: testTime, CreatedAt: testTime,
	})
	assert.ErrorIs(t, err, ErrDuplicateKey)
}
