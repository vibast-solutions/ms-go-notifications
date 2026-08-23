package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// summaryCols are the columns ListForAdmin selects, in order.
var summaryCols = []string{
	"id", "status", "from_email", "from_name", "subject", "recipient_count",
	"first_to", "api_key_name", "provider", "attempts", "created_at", "sent_at",
}

func TestListForAdminNoFilters(t *testing.T) {
	repo, mock := newMessageRepo(t)

	mock.ExpectQuery("SELECT m.id.*FROM messages m").
		WithArgs(26). // only the limit
		WillReturnRows(sqlmock.NewRows(summaryCols).
			AddRow("01MSG", "sent", "no-reply@vibast.ro", nil, "Hi", 2,
				strp("jane@example.com"), "web", strp("ses"), 1, testTime, testTime))

	rows, err := repo.ListForAdmin(context.Background(), MessageFilter{}, 26)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "01MSG", rows[0].ID)
	assert.Equal(t, "jane@example.com", *rows[0].FirstTo)
	assert.Equal(t, "web", rows[0].APIKeyName)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListForAdminAppliesEveryFilterInOrder(t *testing.T) {
	repo, mock := newMessageRepo(t)

	from := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	cursorAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	f := MessageFilter{
		Status:          "sent",
		FromDomain:      "vibast.ro",
		From:            "no-reply",
		Subject:         "receipt",
		APIKeyID:        "01KEY",
		To:              "jane",
		TagPath:         `$."app"`,
		TagValue:        "web",
		CreatedFrom:     &from,
		CreatedTo:       &to,
		BeforeCreatedAt: &cursorAt,
		BeforeID:        "01LAST",
	}

	// Arg order mirrors MessageFilter.build, then the trailing LIMIT.
	mock.ExpectQuery("SELECT m.id.*FROM messages m").
		WithArgs("sent", "vibast.ro", "%no-reply%", "%receipt%", "01KEY", "%jane%",
			`$."app"`, "web", from, to, cursorAt, cursorAt, "01LAST", 10).
		WillReturnRows(sqlmock.NewRows(summaryCols))

	_, err := repo.ListForAdmin(context.Background(), f, 10)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRequeueForRetrySuccess(t *testing.T) {
	repo, mock := newMessageRepo(t)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE messages")).
		WithArgs(testTime, "01MSG").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO message_events")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ev := &entity.MessageEvent{ID: "01EV", MessageID: "01MSG", Type: entity.EventTypeRetryRequested, OccurredAt: testTime, CreatedAt: testTime}
	ok, err := repo.RequeueForRetry(context.Background(), "01MSG", ev, testTime)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRequeueForRetryNotFailedRollsBack(t *testing.T) {
	repo, mock := newMessageRepo(t)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE messages")).
		WithArgs(testTime, "01MSG").
		WillReturnResult(sqlmock.NewResult(0, 0)) // guard did not match
	mock.ExpectRollback()

	ev := &entity.MessageEvent{ID: "01EV", MessageID: "01MSG", Type: entity.EventTypeRetryRequested, OccurredAt: testTime, CreatedAt: testTime}
	ok, err := repo.RequeueForRetry(context.Background(), "01MSG", ev, testTime)
	require.NoError(t, err)
	assert.False(t, ok, "no row was re-queued")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFindAttachmentPurged(t *testing.T) {
	repo, mock := newMessageRepo(t)

	purgedAt := testTime
	mock.ExpectQuery(regexp.QuoteMeta("FROM message_attachments WHERE id = ? AND message_id = ?")).
		WithArgs("01ATT", "01MSG").
		WillReturnRows(sqlmock.NewRows([]string{"id", "message_id", "filename", "content_type", "size_bytes", "content", "purged_at"}).
			AddRow("01ATT", "01MSG", "a.pdf", "application/pdf", 1234, nil, purgedAt))

	att, err := repo.FindAttachment(context.Background(), "01MSG", "01ATT")
	require.NoError(t, err)
	assert.Nil(t, att.Content)
	require.NotNil(t, att.PurgedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFindAttachmentNotFound(t *testing.T) {
	repo, mock := newMessageRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta("FROM message_attachments WHERE id = ? AND message_id = ?")).
		WithArgs("01ATT", "01MSG").
		WillReturnError(sql.ErrNoRows)

	_, err := repo.FindAttachment(context.Background(), "01MSG", "01ATT")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestCountMessagesByStatus(t *testing.T) {
	repo, mock := newMessageRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT status, COUNT(*) FROM messages WHERE status IN (?,?) GROUP BY status")).
		WithArgs("queued", "sending").
		WillReturnRows(sqlmock.NewRows([]string{"status", "n"}).
			AddRow("queued", 7).AddRow("sending", 2))

	got, err := repo.CountMessagesByStatus(context.Background(), []string{"queued", "sending"})
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"queued": 7, "sending": 2}, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCountMessagesByStatusEmptyShortCircuits(t *testing.T) {
	repo, _ := newMessageRepo(t)
	got, err := repo.CountMessagesByStatus(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestStatsMessageBucketsGroupsByBucketAndStatus(t *testing.T) {
	repo, mock := newMessageRepo(t)

	from := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery("DATE_FORMAT.*FROM messages WHERE").
		WithArgs(from, to, "vibast.ro").
		WillReturnRows(sqlmock.NewRows([]string{"bucket", "status", "count"}).
			AddRow("2026-08-20T00:00:00Z", "sent", 10).
			AddRow("2026-08-20T00:00:00Z", "failed", 1))

	rows, err := repo.StatsMessageBuckets(context.Background(), from, to, "vibast.ro", "day")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "sent", rows[0].Status)
	assert.Equal(t, 10, rows[0].Count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStatsEventBucketsSplitsBounces(t *testing.T) {
	repo, mock := newMessageRepo(t)

	from := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)

	// No fromDomain → no join to messages, only the two time bounds.
	mock.ExpectQuery("FROM message_events e").
		WithArgs(from, to).
		WillReturnRows(sqlmock.NewRows([]string{"bucket", "delivered", "bounced_hard", "bounced_soft", "complained"}).
			AddRow("2026-08-20T00:00:00Z", 8, 1, 2, 1))

	rows, err := repo.StatsEventBuckets(context.Background(), from, to, "", "day")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 1, rows[0].BouncedHard)
	assert.Equal(t, 2, rows[0].BouncedSoft)
	assert.NoError(t, mock.ExpectationsWereMet())
}
