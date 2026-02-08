package controller

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/labstack/echo/v4"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/queue"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

type noopLocker struct{}

func (l noopLocker) Acquire(_ context.Context, _ string, _ time.Duration) error { return nil }
func (l noopLocker) Release(_ context.Context, _ string) error                  { return nil }

type noopPreparer struct{}

func (p noopPreparer) Prepare(_ context.Context, _ string, _ string, _ string) ([]byte, error) {
	return []byte("raw"), nil
}

type noopProvider struct{}

func (p noopProvider) SendRaw(_ context.Context, _ string, _ []byte) error { return nil }

type mockPublisher struct {
	err      error
	messages []queue.EmailMessage
}

func (p *mockPublisher) Publish(_ context.Context, msg queue.EmailMessage) error {
	if p.err != nil {
		return p.err
	}
	p.messages = append(p.messages, msg)
	return nil
}

func TestEmailControllerSendRawSuccess(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO email_history").
		WithArgs("req-1", "a@b.com", "subj", "content-long", entity.EmailStatusNew).
		WillReturnResult(sqlmock.NewResult(1, 1))

	emailService := service.NewEmailService(noopPreparer{}, noopProvider{}, repository.NewEmailHistoryRepository(db), noopLocker{})
	pub := &mockPublisher{}
	ctrl := NewEmailController(emailService, pub)

	e := echo.New()
	body := `{"request_id":"req-1","recipient":"a@b.com","subject":"subj","content":"content-long"}`
	req := httptest.NewRequest(http.MethodPost, "/email/send/raw", bytes.NewBufferString(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	if err := ctrl.SendRaw(ctx); err != nil {
		t.Fatalf("SendRaw: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if len(pub.messages) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(pub.messages))
	}
	if pub.messages[0].RequestID != "req-1" {
		t.Fatalf("expected request_id req-1, got %s", pub.messages[0].RequestID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEmailControllerSendRawDuplicate(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mysqlErr := &mysql.MySQLError{Number: 1062}
	mock.ExpectExec("INSERT INTO email_history").
		WithArgs("req-dup", "a@b.com", "subj", "content-long", entity.EmailStatusNew).
		WillReturnError(mysqlErr)

	emailService := service.NewEmailService(noopPreparer{}, noopProvider{}, repository.NewEmailHistoryRepository(db), noopLocker{})
	pub := &mockPublisher{}
	ctrl := NewEmailController(emailService, pub)

	e := echo.New()
	body := `{"request_id":"req-dup","recipient":"a@b.com","subject":"subj","content":"content-long"}`
	req := httptest.NewRequest(http.MethodPost, "/email/send/raw", bytes.NewBufferString(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	if err := ctrl.SendRaw(ctx); err != nil {
		t.Fatalf("SendRaw: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	if len(pub.messages) != 0 {
		t.Fatalf("expected 0 published messages, got %d", len(pub.messages))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEmailControllerSendRawPublishFailureDeletesRequest(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO email_history").
		WithArgs("req-1", "a@b.com", "subj", "content-long", entity.EmailStatusNew).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("DELETE FROM email_history").
		WithArgs("req-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	emailService := service.NewEmailService(noopPreparer{}, noopProvider{}, repository.NewEmailHistoryRepository(db), noopLocker{})
	pub := &mockPublisher{err: errors.New("publish failed")}
	ctrl := NewEmailController(emailService, pub)

	e := echo.New()
	body := `{"request_id":"req-1","recipient":"a@b.com","subject":"subj","content":"content-long"}`
	req := httptest.NewRequest(http.MethodPost, "/email/send/raw", bytes.NewBufferString(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	if err := ctrl.SendRaw(ctx); err != nil {
		t.Fatalf("SendRaw: %v", err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEmailControllerSendRawValidationError(t *testing.T) {
	t.Parallel()

	emailService := service.NewEmailService(noopPreparer{}, noopProvider{}, repository.NewEmailHistoryRepository(nil), noopLocker{})
	pub := &mockPublisher{}
	ctrl := NewEmailController(emailService, pub)

	e := echo.New()
	body := `{"request_id":"1","recipient":"bad","subject":"abcd","content":"long enough!"}`
	req := httptest.NewRequest(http.MethodPost, "/email/send/raw", bytes.NewBufferString(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	if err := ctrl.SendRaw(ctx); err != nil {
		t.Fatalf("SendRaw: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestEmailControllerSendRawInvalidBody(t *testing.T) {
	t.Parallel()

	emailService := service.NewEmailService(noopPreparer{}, noopProvider{}, repository.NewEmailHistoryRepository(nil), noopLocker{})
	pub := &mockPublisher{}
	ctrl := NewEmailController(emailService, pub)

	e := echo.New()
	body := `not json`
	req := httptest.NewRequest(http.MethodPost, "/email/send/raw", bytes.NewBufferString(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	if err := ctrl.SendRaw(ctx); err != nil {
		t.Fatalf("SendRaw: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
