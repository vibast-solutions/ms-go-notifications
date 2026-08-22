package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

var testTime = time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)

func newMockRepo(t *testing.T) (*APIKeyRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewAPIKeyRepository(db), mock
}

func testKey() *entity.APIKey {
	return &entity.APIKey{
		ID:        "01KEY",
		Name:      "test",
		KeyPrefix: "abcd1234",
		KeyHash:   "hash",
		Scopes:    []string{"send"},
		Status:    entity.APIKeyStatusActive,
		CreatedAt: testTime,
	}
}

func TestCreateInsertsKeyAndDomainsInOneTx(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO api_keys")).
		WithArgs("01KEY", "test", "abcd1234", "hash", `["send"]`, "active", testTime).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO api_key_domains")).
		WithArgs("01KEY", "a.com", testTime).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO api_key_domains")).
		WithArgs("01KEY", "b.com", testTime).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Create(context.Background(), testKey(), []string{"a.com", "b.com"})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRollsBackWhenDomainInsertFails(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO api_keys")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO api_key_domains")).
		WillReturnError(errors.New("duplicate entry"))
	mock.ExpectRollback()

	err := repo.Create(context.Background(), testKey(), []string{"a.com"})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func keyColumns() []string {
	return []string{"id", "name", "key_prefix", "key_hash", "scopes", "status",
		"created_at", "last_used_at", "revoked_at", "domain"}
}

func TestFindByPrefixFoldsDomains(t *testing.T) {
	repo, mock := newMockRepo(t)

	rows := sqlmock.NewRows(keyColumns()).
		AddRow("01KEY", "test", "abcd1234", "hash", `["send"]`, "active", testTime, nil, nil, "a.com").
		AddRow("01KEY", "test", "abcd1234", "hash", `["send"]`, "active", testTime, nil, nil, "b.com")
	mock.ExpectQuery("SELECT .+ FROM api_keys k").WithArgs("abcd1234").WillReturnRows(rows)

	key, err := repo.FindByPrefix(context.Background(), "abcd1234")
	require.NoError(t, err)
	assert.Equal(t, "01KEY", key.ID)
	assert.Equal(t, []string{"send"}, key.Scopes)
	assert.Equal(t, []string{"a.com", "b.com"}, key.Domains)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFindByPrefixNotFound(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery("SELECT .+ FROM api_keys k").WithArgs("zzzzzzzz").
		WillReturnRows(sqlmock.NewRows(keyColumns()))

	_, err := repo.FindByPrefix(context.Background(), "zzzzzzzz")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestListGroupsDomainsPerKey(t *testing.T) {
	repo, mock := newMockRepo(t)

	rows := sqlmock.NewRows(keyColumns()).
		AddRow("01A", "send key", "aaaaaaaa", "h1", `["send"]`, "active", testTime, nil, nil, "a.com").
		AddRow("01A", "send key", "aaaaaaaa", "h1", `["send"]`, "active", testTime, nil, nil, "b.com").
		AddRow("01B", "admin key", "bbbbbbbb", "h2", `["admin"]`, "active", testTime, nil, nil, nil)
	mock.ExpectQuery("SELECT .+ FROM api_keys k").WillReturnRows(rows)

	keys, err := repo.List(context.Background())
	require.NoError(t, err)
	require.Len(t, keys, 2)
	assert.Equal(t, []string{"a.com", "b.com"}, keys[0].Domains)
	assert.Empty(t, keys[1].Domains)
}

func TestRevoke(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE api_keys SET status")).
		WithArgs("revoked", testTime, "01KEY", "active").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.Revoke(context.Background(), "01KEY", testTime))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRevokeUnknownIDReturnsNoRows(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE api_keys SET status")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	assert.ErrorIs(t, repo.Revoke(context.Background(), "nope", testTime), sql.ErrNoRows)
}

func TestReplaceDomainsSwapsInOneTx(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM api_keys WHERE id = ?")).
		WithArgs("01KEY").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM api_key_domains WHERE api_key_id = ?")).
		WithArgs("01KEY").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO api_key_domains")).
		WithArgs("01KEY", "c.com", testTime).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.ReplaceDomains(context.Background(), "01KEY", []string{"c.com"}, testTime)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReplaceDomainsUnknownID(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM api_keys WHERE id = ?")).
		WithArgs("nope").
		WillReturnRows(sqlmock.NewRows([]string{"1"}))
	mock.ExpectRollback()

	assert.ErrorIs(t, repo.ReplaceDomains(context.Background(), "nope", []string{"c.com"}, testTime), sql.ErrNoRows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTouchLastUsed(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE api_keys SET last_used_at = ? WHERE id = ?")).
		WithArgs(testTime, "01KEY").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.TouchLastUsed(context.Background(), "01KEY", testTime))
	assert.NoError(t, mock.ExpectationsWereMet())
}
