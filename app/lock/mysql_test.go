package lock

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMySQLLockerAcquireRelease(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	locker := NewMySQLLocker(db)
	mock.ExpectQuery("SELECT GET_LOCK").
		WithArgs("lock-key", 120).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(1))

	if err := locker.Acquire(context.Background(), "lock-key", 2*time.Minute); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	mock.ExpectExec("SELECT RELEASE_LOCK").
		WithArgs("lock-key").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := locker.Release(context.Background(), "lock-key"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestMySQLLockerNotAcquired(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	locker := NewMySQLLocker(db)
	mock.ExpectQuery("SELECT GET_LOCK").
		WithArgs("lock-key", 120).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(0))

	if err := locker.Acquire(context.Background(), "lock-key", 2*time.Minute); err != ErrNotAcquired {
		t.Fatalf("expected ErrNotAcquired, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
