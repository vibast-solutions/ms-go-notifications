package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaimWinsAndLoses(t *testing.T) {
	repo, mock := newMessageRepo(t)
	ctx := context.Background()

	// Won the claim: exactly one row changed.
	mock.ExpectExec(regexp.QuoteMeta("UPDATE messages")).
		WithArgs(testTime, "host:1", testTime, "01MSG", testTime).
		WillReturnResult(sqlmock.NewResult(0, 1))
	won, err := repo.Claim(ctx, "01MSG", "host:1", testTime)
	require.NoError(t, err)
	assert.True(t, won)

	// Lost the claim: zero rows (another worker owns it or it is not due).
	mock.ExpectExec(regexp.QuoteMeta("UPDATE messages")).
		WithArgs(testTime, "host:1", testTime, "01MSG", testTime).
		WillReturnResult(sqlmock.NewResult(0, 0))
	won, err = repo.Claim(ctx, "01MSG", "host:1", testTime)
	require.NoError(t, err)
	assert.False(t, won)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkSent(t *testing.T) {
	repo, mock := newMessageRepo(t)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE messages")).
		WithArgs("ses", "ses-123", testTime, testTime, testTime, "01MSG").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkSent(context.Background(), "01MSG", "ses", "ses-123", testTime))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkRetry(t *testing.T) {
	repo, mock := newMessageRepo(t)
	next := testTime.Add(10 * time.Second)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE messages")).
		WithArgs("ses", "timeout", next, testTime, "01MSG").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkRetry(context.Background(), "01MSG", "ses", "timeout", next, testTime))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkFailed(t *testing.T) {
	repo, mock := newMessageRepo(t)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE messages")).
		WithArgs("ses", "rejected", testTime, testTime, "01MSG").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkFailed(context.Background(), "01MSG", "ses", "rejected", testTime))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecipientsStatus(t *testing.T) {
	repo, mock := newMessageRepo(t)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE message_recipients SET status=? WHERE message_id=? AND status=?")).
		WithArgs("sent", "01MSG", "pending").
		WillReturnResult(sqlmock.NewResult(0, 3))
	n, err := repo.UpdateRecipientsStatus(context.Background(), "01MSG", "pending", "sent")
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFindDueQueuedNoExclude(t *testing.T) {
	repo, mock := newMessageRepo(t)
	mock.ExpectQuery("SELECT id FROM messages").
		WithArgs(testTime, 100).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("01A").AddRow("01B"))
	ids, err := repo.FindDueQueued(context.Background(), testTime, 100, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"01A", "01B"}, ids)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFindDueQueuedWithExclude(t *testing.T) {
	repo, mock := newMessageRepo(t)
	mock.ExpectQuery("SELECT id FROM messages .+ id NOT IN").
		WithArgs(testTime, "01X", "01Y", 50).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("01A"))
	ids, err := repo.FindDueQueued(context.Background(), testTime, 50, []string{"01X", "01Y"})
	require.NoError(t, err)
	assert.Equal(t, []string{"01A"}, ids)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUnstickStaleSending(t *testing.T) {
	repo, mock := newMessageRepo(t)
	threshold := testTime.Add(-5 * time.Minute)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE messages")).
		WithArgs(testTime, threshold).
		WillReturnResult(sqlmock.NewResult(0, 2))
	n, err := repo.UnstickStaleSending(context.Background(), threshold, testTime)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPurgeAttachmentsBatch(t *testing.T) {
	repo, mock := newMessageRepo(t)
	cutoff := testTime.Add(-168 * time.Hour)

	mock.ExpectQuery("SELECT a.id FROM message_attachments").
		WithArgs(cutoff, 1000).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("01ATT1").AddRow("01ATT2"))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE message_attachments SET content=NULL, purged_at=? WHERE id IN (?,?)")).
		WithArgs(testTime, "01ATT1", "01ATT2").
		WillReturnResult(sqlmock.NewResult(0, 2))

	n, err := repo.PurgeAttachmentsBatch(context.Background(), cutoff, 1000, testTime)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPurgeAttachmentsBatchEmpty(t *testing.T) {
	repo, mock := newMessageRepo(t)
	cutoff := testTime.Add(-168 * time.Hour)
	mock.ExpectQuery("SELECT a.id FROM message_attachments").
		WithArgs(cutoff, 1000).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	n, err := repo.PurgeAttachmentsBatch(context.Background(), cutoff, 1000, testTime)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteHistoryBatch(t *testing.T) {
	repo, mock := newMessageRepo(t)
	cutoff := testTime.Add(-720 * time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM messages WHERE created_at").
		WithArgs(cutoff, 1000).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("01M1").AddRow("01M2"))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM message_events WHERE message_id IN (?,?)")).
		WithArgs("01M1", "01M2").WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM message_recipients WHERE message_id IN (?,?)")).
		WithArgs("01M1", "01M2").WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM message_attachments WHERE message_id IN (?,?)")).
		WithArgs("01M1", "01M2").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM messages WHERE id IN (?,?)")).
		WithArgs("01M1", "01M2").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	n, err := repo.DeleteHistoryBatch(context.Background(), cutoff, 1000)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteHistoryBatchEmpty(t *testing.T) {
	repo, mock := newMessageRepo(t)
	cutoff := testTime.Add(-720 * time.Hour)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM messages WHERE created_at").
		WithArgs(cutoff, 1000).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()

	n, err := repo.DeleteHistoryBatch(context.Background(), cutoff, 1000)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadAttachments(t *testing.T) {
	repo, mock := newMessageRepo(t)
	mock.ExpectQuery("SELECT .+ FROM message_attachments WHERE message_id = \\?").
		WithArgs("01MSG").
		WillReturnRows(sqlmock.NewRows([]string{"id", "message_id", "filename", "content_type", "size_bytes", "content", "purged_at"}).
			AddRow("01A", "01MSG", "a.txt", "text/plain", 5, []byte("hello"), nil))
	atts, err := repo.LoadAttachments(context.Background(), "01MSG")
	require.NoError(t, err)
	require.Len(t, atts, 1)
	assert.Equal(t, "a.txt", atts[0].Filename)
	assert.Equal(t, []byte("hello"), atts[0].Content)
}
