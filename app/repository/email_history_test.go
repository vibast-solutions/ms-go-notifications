package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestEmailHistoryRepositoryCRUD(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewEmailHistoryRepository(db)

	mock.ExpectExec("INSERT INTO email_history").
		WithArgs("req-1", "a@b.com", "subj", "content", int16(0)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := repo.Create(context.Background(), "req-1", "a@b.com", "subj", "content", 0); err != nil {
		t.Fatalf("Create: %v", err)
	}

	mock.ExpectExec("UPDATE email_history").
		WithArgs(int16(1), "req-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.UpdateStatus(context.Background(), "req-1", 1); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	mock.ExpectExec("UPDATE email_history").
		WithArgs("raw", "req-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.UpdateContent(context.Background(), "req-1", "raw"); err != nil {
		t.Fatalf("UpdateContent: %v", err)
	}

	mock.ExpectExec("DELETE FROM email_history").
		WithArgs("req-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.DeleteByRequestID(context.Background(), "req-1"); err != nil {
		t.Fatalf("DeleteByRequestID: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
