package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

func newMessageRepo(t *testing.T) (*MessageRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewMessageRepository(db), mock
}

func sampleMessage() *entity.Message {
	return &entity.Message{
		ID: "01MSG", APIKeyID: "01KEY", Channel: entity.ChannelEmail,
		Status: entity.MessageStatusQueued, FromEmail: "no-reply@vibast.ro",
		FromDomain: "vibast.ro", Subject: "Hi", TextBody: strp("hello"),
		Tags: map[string]string{"app": "web"}, RecipientCount: 1,
		CreatedAt: testTime, UpdatedAt: testTime,
	}
}

func TestCreateWithChildrenInsertsAllRows(t *testing.T) {
	repo, mock := newMessageRepo(t)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO messages")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO message_recipients")).
		WithArgs("01R", "01MSG", "to", "jane@example.com", nil, "pending").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO message_attachments")).
		WithArgs("01A", "01MSG", "a.txt", "text/plain", 5, []byte("hello")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO message_events")).
		WithArgs("01E", "01MSG", nil, "queued", nil, nil, testTime, testTime).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	tx, err := repo.BeginTx(context.Background())
	require.NoError(t, err)
	err = repo.CreateWithChildren(context.Background(), tx, sampleMessage(),
		[]*entity.MessageRecipient{{ID: "01R", MessageID: "01MSG", Type: "to", Email: "jane@example.com", Status: "pending"}},
		[]*entity.MessageAttachment{{ID: "01A", MessageID: "01MSG", Filename: "a.txt", ContentType: "text/plain", SizeBytes: 5, Content: []byte("hello")}},
		[]*entity.MessageEvent{{ID: "01E", MessageID: "01MSG", Type: "queued", OccurredAt: testTime, CreatedAt: testTime}},
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateWithChildrenMapsDuplicateKey(t *testing.T) {
	repo, mock := newMessageRepo(t)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO messages")).
		WillReturnError(&mysql.MySQLError{Number: 1062, Message: "duplicate"})

	tx, err := repo.BeginTx(context.Background())
	require.NoError(t, err)
	err = repo.CreateWithChildren(context.Background(), tx, sampleMessage(), nil, nil, nil)
	assert.ErrorIs(t, err, ErrDuplicateKey)
	_ = tx.Rollback()
}

func messageColumns() []string {
	return []string{
		"id", "api_key_id", "channel", "status", "provider", "provider_message_id",
		"from_email", "from_name", "from_domain", "reply_to", "subject", "text_body", "html_body",
		"headers", "tags", "metadata", "idempotency_key", "attempts", "last_error",
		"next_attempt_at", "locked_at", "locked_by", "recipient_count",
		"created_at", "updated_at", "sent_at", "finished_at",
	}
}

func TestFindByIDForAPIKeyScansJSONColumns(t *testing.T) {
	repo, mock := newMessageRepo(t)

	rows := sqlmock.NewRows(messageColumns()).AddRow(
		"01MSG", "01KEY", "email", "queued", nil, nil,
		"no-reply@vibast.ro", nil, "vibast.ro", []byte(`[{"email":"r@vibast.ro"}]`), "Hi", nil, nil,
		[]byte(`{"X-Ref":"1"}`), []byte(`{"app":"web"}`), []byte(`{"n":1}`), nil, 0, nil,
		nil, nil, nil, 1,
		testTime, testTime, nil, nil,
	)
	mock.ExpectQuery("SELECT .+ FROM messages WHERE id = \\? AND api_key_id = \\?").
		WithArgs("01MSG", "01KEY").WillReturnRows(rows)

	msg, err := repo.FindByIDForAPIKey(context.Background(), "01MSG", "01KEY")
	require.NoError(t, err)
	assert.Equal(t, "01MSG", msg.ID)
	assert.Equal(t, "vibast.ro", msg.FromDomain)
	require.Len(t, msg.ReplyTo, 1)
	assert.Equal(t, "r@vibast.ro", msg.ReplyTo[0].Email)
	assert.Equal(t, map[string]string{"X-Ref": "1"}, msg.Headers)
	assert.Equal(t, map[string]string{"app": "web"}, msg.Tags)
	assert.Equal(t, json.RawMessage(`{"n":1}`), msg.Metadata)
}

func TestFindByProviderMessageID(t *testing.T) {
	repo, mock := newMessageRepo(t)

	rows := sqlmock.NewRows(messageColumns()).AddRow(
		"01MSG", "01KEY", "email", "sent", "ses", "ses-msg-1",
		"no-reply@vibast.ro", nil, "vibast.ro", nil, "Hi", nil, nil,
		nil, nil, nil, nil, 1, nil,
		nil, nil, nil, 1,
		testTime, testTime, testTime, nil,
	)
	mock.ExpectQuery("SELECT .+ FROM messages WHERE provider_message_id = \\? ORDER BY created_at DESC LIMIT 1").
		WithArgs("ses-msg-1").WillReturnRows(rows)

	msg, err := repo.FindByProviderMessageID(context.Background(), "ses-msg-1")
	require.NoError(t, err)
	assert.Equal(t, "01MSG", msg.ID)
	require.NotNil(t, msg.ProviderMessageID)
	assert.Equal(t, "ses-msg-1", *msg.ProviderMessageID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFindByProviderMessageIDNotFound(t *testing.T) {
	repo, mock := newMessageRepo(t)
	mock.ExpectQuery("SELECT .+ FROM messages WHERE provider_message_id = \\?").
		WithArgs("missing").WillReturnError(sql.ErrNoRows)

	_, err := repo.FindByProviderMessageID(context.Background(), "missing")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestSetRecipientStatus(t *testing.T) {
	repo, mock := newMessageRepo(t)
	bt, bst, diag := "Permanent", "General", "smtp; 550"
	mock.ExpectExec(regexp.QuoteMeta("UPDATE message_recipients")).
		WithArgs("bounced", &bt, &bst, &diag, testTime, "01MSG", "bad@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.SetRecipientStatus(context.Background(), "01MSG", "bad@example.com", "bounced",
		&bt, &bst, &diag, testTime)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSetLastError(t *testing.T) {
	repo, mock := newMessageRepo(t)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE messages SET last_error = ?, updated_at = ? WHERE id = ?")).
		WithArgs("Bad content", testTime, "01MSG").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.SetLastError(context.Background(), "01MSG", "Bad content", testTime)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFindByIdempotencyNotFound(t *testing.T) {
	repo, mock := newMessageRepo(t)
	mock.ExpectQuery("SELECT .+ FROM messages WHERE api_key_id = \\? AND idempotency_key = \\?").
		WithArgs("01KEY", "dup").WillReturnError(sql.ErrNoRows)

	_, err := repo.FindByIdempotency(context.Background(), "01KEY", "dup")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestLoadRecipientsAndEvents(t *testing.T) {
	repo, mock := newMessageRepo(t)

	mock.ExpectQuery("SELECT .+ FROM message_recipients WHERE message_id = \\?").
		WithArgs("01MSG").
		WillReturnRows(sqlmock.NewRows([]string{"id", "message_id", "type", "email", "name", "status",
			"bounce_type", "bounce_subtype", "diagnostic", "last_event_at"}).
			AddRow("01R", "01MSG", "to", "jane@example.com", nil, "delivered", nil, nil, nil, nil))
	recipients, err := repo.LoadRecipients(context.Background(), "01MSG")
	require.NoError(t, err)
	require.Len(t, recipients, 1)
	assert.Equal(t, "jane@example.com", recipients[0].Email)

	mock.ExpectQuery("SELECT .+ FROM message_events WHERE message_id = \\?").
		WithArgs("01MSG").
		WillReturnRows(sqlmock.NewRows([]string{"id", "message_id", "recipient_email", "type",
			"provider_event_id", "payload", "occurred_at", "created_at"}).
			AddRow("01E", "01MSG", nil, "queued", nil, []byte(`{"attempt":1}`), testTime, testTime))
	events, err := repo.LoadEvents(context.Background(), "01MSG")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, json.RawMessage(`{"attempt":1}`), events[0].Payload)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func strp(s string) *string { return &s }
