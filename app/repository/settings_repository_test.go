package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

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

func TestSettingsGetReturnsRow(t *testing.T) {
	repo, mock := newSettingsRepo(t)
	updated := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT `key`, value, updated_at FROM settings WHERE").
		WithArgs("sending_paused").
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updated_at"}).
			AddRow("sending_paused", []byte(`{"reason":"manual"}`), updated))

	s, err := repo.Get(context.Background(), "sending_paused")
	require.NoError(t, err)
	assert.Equal(t, "sending_paused", s.Key)
	assert.JSONEq(t, `{"reason":"manual"}`, string(s.Value))
	assert.Equal(t, updated, s.UpdatedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSettingsGetMissingReturnsErrNoRows(t *testing.T) {
	repo, mock := newSettingsRepo(t)
	mock.ExpectQuery("SELECT `key`, value, updated_at FROM settings WHERE").
		WithArgs("sending_paused").
		WillReturnError(sql.ErrNoRows)

	_, err := repo.Get(context.Background(), "sending_paused")
	assert.True(t, errors.Is(err, sql.ErrNoRows), "missing row surfaces as sql.ErrNoRows")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSettingsUpsert(t *testing.T) {
	repo, mock := newSettingsRepo(t)
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	value := json.RawMessage(`{"reason":"auto: bounce 4.3% >= 4%","by":"auto"}`)
	mock.ExpectExec("INSERT INTO settings").
		WithArgs("sending_paused", []byte(value), now).
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, repo.Upsert(context.Background(), "sending_paused", value, now))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSettingsDelete(t *testing.T) {
	repo, mock := newSettingsRepo(t)
	mock.ExpectExec("DELETE FROM settings WHERE").
		WithArgs("sending_paused").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.Delete(context.Background(), "sending_paused"))
	assert.NoError(t, mock.ExpectationsWereMet())
}
