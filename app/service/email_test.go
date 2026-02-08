package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
)

type fakeLocker struct {
	acquireErr error
	acquired   []string
	released   []string
}

func (l *fakeLocker) Acquire(_ context.Context, key string, _ time.Duration) error {
	if l.acquireErr != nil {
		return l.acquireErr
	}
	l.acquired = append(l.acquired, key)
	return nil
}

func (l *fakeLocker) Release(_ context.Context, key string) error {
	l.released = append(l.released, key)
	return nil
}

type fakePreparer struct {
	raw []byte
	err error
}

func (p fakePreparer) Prepare(_ context.Context, _ string, _ string, _ string) ([]byte, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.raw, nil
}

type fakeProvider struct {
	err error
}

func (p fakeProvider) SendRaw(_ context.Context, _ string, _ []byte) error {
	return p.err
}

func newRepo(t *testing.T) (*repository.EmailHistoryRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return repository.NewEmailHistoryRepository(db), mock, func() { _ = db.Close() }
}

func TestEmailServiceCreateRequestDuplicate(t *testing.T) {
	t.Parallel()

	repo, mock, cleanup := newRepo(t)
	defer cleanup()

	prep := fakePreparer{}
	prov := fakeProvider{}
	locker := &fakeLocker{}
	svc := NewEmailService(prep, prov, repo, locker)

	mysqlErr := &mysql.MySQLError{Number: 1062}
	mock.ExpectExec("INSERT INTO email_history").
		WithArgs("req-1", "a@b.com", "subj", "content", entity.EmailStatusNew).
		WillReturnError(mysqlErr)

	if err := svc.CreateRequest(context.Background(), "req-1", "a@b.com", "subj", "content"); !errors.Is(err, ErrDuplicateRequestID) {
		t.Fatalf("expected ErrDuplicateRequestID, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEmailServiceSendRawSuccess(t *testing.T) {
	t.Parallel()

	repo, mock, cleanup := newRepo(t)
	defer cleanup()

	prep := fakePreparer{raw: []byte("raw")}
	prov := fakeProvider{}
	locker := &fakeLocker{}
	svc := NewEmailService(prep, prov, repo, locker)

	requestID := "req-1"
	mock.ExpectExec("UPDATE email_history").
		WithArgs(entity.EmailStatusProcessing, requestID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE email_history").
		WithArgs("raw", requestID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE email_history").
		WithArgs(entity.EmailStatusSuccess, requestID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	ctx := WithRequestID(context.Background(), requestID)
	if err := svc.SendRaw(ctx, "a@b.com", "subj", "content"); err != nil {
		t.Fatalf("SendRaw returned error: %v", err)
	}

	if len(locker.acquired) != 1 || len(locker.released) != 1 {
		t.Fatalf("expected lock acquire/release, got acquired=%v released=%v", locker.acquired, locker.released)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEmailServiceSendRawPrepareFailure(t *testing.T) {
	t.Parallel()

	repo, mock, cleanup := newRepo(t)
	defer cleanup()

	prep := fakePreparer{err: errors.New("prepare failed")}
	prov := fakeProvider{}
	locker := &fakeLocker{}
	svc := NewEmailService(prep, prov, repo, locker)

	requestID := "req-2"
	mock.ExpectExec("UPDATE email_history").
		WithArgs(entity.EmailStatusProcessing, requestID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE email_history").
		WithArgs(entity.EmailStatusTemporaryFailure, requestID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	ctx := WithRequestID(context.Background(), requestID)
	if err := svc.SendRaw(ctx, "a@b.com", "subj", "content"); err == nil {
		t.Fatalf("expected error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEmailServiceSendRawUpdateContentFailure(t *testing.T) {
	t.Parallel()

	repo, mock, cleanup := newRepo(t)
	defer cleanup()

	prep := fakePreparer{raw: []byte("raw")}
	prov := fakeProvider{}
	locker := &fakeLocker{}
	svc := NewEmailService(prep, prov, repo, locker)

	requestID := "req-3"
	mock.ExpectExec("UPDATE email_history").
		WithArgs(entity.EmailStatusProcessing, requestID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE email_history").
		WithArgs("raw", requestID).
		WillReturnError(errors.New("update content failed"))
	mock.ExpectExec("UPDATE email_history").
		WithArgs(entity.EmailStatusTemporaryFailure, requestID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	ctx := WithRequestID(context.Background(), requestID)
	if err := svc.SendRaw(ctx, "a@b.com", "subj", "content"); err == nil {
		t.Fatalf("expected error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEmailServiceSendRawProviderFailure(t *testing.T) {
	t.Parallel()

	repo, mock, cleanup := newRepo(t)
	defer cleanup()

	prep := fakePreparer{raw: []byte("raw")}
	prov := fakeProvider{err: errors.New("send failed")}
	locker := &fakeLocker{}
	svc := NewEmailService(prep, prov, repo, locker)

	requestID := "req-4"
	mock.ExpectExec("UPDATE email_history").
		WithArgs(entity.EmailStatusProcessing, requestID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE email_history").
		WithArgs("raw", requestID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE email_history").
		WithArgs(entity.EmailStatusPermanentFailure, requestID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	ctx := WithRequestID(context.Background(), requestID)
	if err := svc.SendRaw(ctx, "a@b.com", "subj", "content"); err == nil {
		t.Fatalf("expected error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEmailServiceSendRawLockFailure(t *testing.T) {
	t.Parallel()

	repo, mock, cleanup := newRepo(t)
	defer cleanup()

	prep := fakePreparer{raw: []byte("raw")}
	prov := fakeProvider{}
	locker := &fakeLocker{acquireErr: errors.New("lock failed")}
	svc := NewEmailService(prep, prov, repo, locker)

	ctx := WithRequestID(context.Background(), "req-5")
	if err := svc.SendRaw(ctx, "a@b.com", "subj", "content"); err == nil {
		t.Fatalf("expected error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEmailServiceSendRawMissingRequestID(t *testing.T) {
	t.Parallel()

	repo, mock, cleanup := newRepo(t)
	defer cleanup()

	svc := NewEmailService(fakePreparer{}, fakeProvider{}, repo, &fakeLocker{})

	if err := svc.SendRaw(context.Background(), "a@b.com", "subj", "content"); err == nil {
		t.Fatalf("expected error for missing request_id")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEmailServiceSendRawEmptyRecipient(t *testing.T) {
	t.Parallel()

	repo, mock, cleanup := newRepo(t)
	defer cleanup()

	svc := NewEmailService(fakePreparer{}, fakeProvider{}, repo, &fakeLocker{})

	ctx := WithRequestID(context.Background(), "req-6")
	if err := svc.SendRaw(ctx, "", "subj", "content"); err == nil {
		t.Fatalf("expected error for empty recipient")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
