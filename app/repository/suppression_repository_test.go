package repository

import (
	"context"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindByEmailsEmptyShortCircuits(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewSuppressionRepository(db)
	got, err := repo.FindByEmails(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.NoError(t, mock.ExpectationsWereMet(), "empty input must not query the database")
}

func TestFindByEmailsReturnsMatches(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewSuppressionRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta("FROM suppressions WHERE email IN (?,?)")).
		WithArgs("a@x.com", "b@x.com").
		WillReturnRows(sqlmock.NewRows([]string{"email", "reason", "source_message_id", "note", "created_at", "expires_at"}).
			AddRow("a@x.com", "hard_bounce", nil, nil, testTime, nil))

	got, err := repo.FindByEmails(context.Background(), []string{"a@x.com", "b@x.com"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a@x.com", got[0].Email)
	assert.Equal(t, "hard_bounce", got[0].Reason)
	assert.NoError(t, mock.ExpectationsWereMet())
}
