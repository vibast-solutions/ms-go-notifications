package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

func newSuppressionRepo(t *testing.T) (*SuppressionRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewSuppressionRepository(db), mock
}

func TestSuppressionListForAdminFiltersAndCursor(t *testing.T) {
	repo, mock := newSuppressionRepo(t)
	cursorAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	f := SuppressionFilter{
		Email:           "example",
		Reason:          entity.SuppressionReasonManual,
		BeforeCreatedAt: &cursorAt,
		BeforeEmail:     "z@example.com",
	}
	mock.ExpectQuery("FROM suppressions WHERE").
		WithArgs("%example%", "manual", cursorAt, cursorAt, "z@example.com", 5).
		WillReturnRows(sqlmock.NewRows([]string{"email", "reason", "source_message_id", "note", "created_at", "expires_at"}).
			AddRow("a@example.com", "manual", nil, nil, testTime, nil))

	rows, err := repo.ListForAdmin(context.Background(), f, 5)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "a@example.com", rows[0].Email)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSuppressionInsertDuplicateMapsToErrDuplicateKey(t *testing.T) {
	repo, mock := newSuppressionRepo(t)

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO suppressions")).
		WillReturnError(&mysql.MySQLError{Number: mysqlErrDupEntry, Message: "duplicate"})

	err := repo.Insert(context.Background(), &entity.Suppression{
		Email: "dup@example.com", Reason: entity.SuppressionReasonManual, CreatedAt: testTime,
	})
	assert.ErrorIs(t, err, ErrDuplicateKey)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSuppressionDeleteNotFound(t *testing.T) {
	repo, mock := newSuppressionRepo(t)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM suppressions WHERE email = ?")).
		WithArgs("missing@example.com").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.Delete(context.Background(), "missing@example.com")
	assert.ErrorIs(t, err, sql.ErrNoRows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSuppressionDeleteOK(t *testing.T) {
	repo, mock := newSuppressionRepo(t)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM suppressions WHERE email = ?")).
		WithArgs("gone@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.Delete(context.Background(), "gone@example.com")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
