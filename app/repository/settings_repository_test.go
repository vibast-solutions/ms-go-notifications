package repository

import (
	"context"
	"database/sql"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSettingsRepo(t *testing.T) (*SettingsRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewSettingsRepository(db), mock
}

func TestSendingPausedRowPresent(t *testing.T) {
	repo, mock := newSettingsRepo(t)
	mock.ExpectQuery("SELECT 1 FROM settings WHERE").
		WithArgs("sending_paused").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	paused, err := repo.SendingPaused(context.Background())
	require.NoError(t, err)
	assert.True(t, paused)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSendingPausedRowAbsent(t *testing.T) {
	repo, mock := newSettingsRepo(t)
	mock.ExpectQuery("SELECT 1 FROM settings WHERE").
		WithArgs("sending_paused").
		WillReturnError(sql.ErrNoRows)
	paused, err := repo.SendingPaused(context.Background())
	require.NoError(t, err)
	assert.False(t, paused)
	assert.NoError(t, mock.ExpectationsWereMet())
}
