package repository

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newReputationRepo(t *testing.T) (*ReputationRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewReputationRepository(db), mock
}

func TestCountSentRecipientsSince(t *testing.T) {
	repo, mock := newReputationRepo(t)
	since := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*FROM message_recipients").
		WithArgs(since).
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(120))

	n, err := repo.CountSentRecipientsSince(context.Background(), since)
	require.NoError(t, err)
	assert.Equal(t, 120, n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCountHardBouncesAndComplaintsSince(t *testing.T) {
	repo, mock := newReputationRepo(t)
	since := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery("FROM message_events").
		WithArgs(since).
		WillReturnRows(sqlmock.NewRows([]string{"hard_bounces", "complaints"}).AddRow(5, 2))

	hard, complaints, err := repo.CountHardBouncesAndComplaintsSince(context.Background(), since)
	require.NoError(t, err)
	assert.Equal(t, 5, hard)
	assert.Equal(t, 2, complaints)
	assert.NoError(t, mock.ExpectationsWereMet())
}
