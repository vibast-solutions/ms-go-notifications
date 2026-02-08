package grpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/queue"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
	types "github.com/vibast-solutions/ms-go-notifications/app/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

func TestSendRawEmailInvalid(t *testing.T) {
	t.Parallel()

	server := NewServer(nil, nil)
	_, err := server.SendRawEmail(context.Background(), &types.SendRawEmailRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestSendRawEmailSuccess(t *testing.T) {
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
	server := NewServer(emailService, pub)

	resp, err := server.SendRawEmail(context.Background(), &types.SendRawEmailRequest{
		RequestId: "req-1",
		Recipient: "a@b.com",
		Subject:   "subj",
		Content:   "content-long",
	})
	if err != nil {
		t.Fatalf("SendRawEmail: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true")
	}

	if len(pub.messages) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(pub.messages))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestSendRawEmailDuplicate(t *testing.T) {
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
	server := NewServer(emailService, pub)

	_, err = server.SendRawEmail(context.Background(), &types.SendRawEmailRequest{
		RequestId: "req-dup",
		Recipient: "a@b.com",
		Subject:   "subj",
		Content:   "content-long",
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists, got %v", err)
	}

	if len(pub.messages) != 0 {
		t.Fatalf("expected 0 published messages, got %d", len(pub.messages))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestSendRawEmailPublishFailureDeletesRequest(t *testing.T) {
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
	server := NewServer(emailService, pub)

	_, err = server.SendRawEmail(context.Background(), &types.SendRawEmailRequest{
		RequestId: "req-1",
		Recipient: "a@b.com",
		Subject:   "subj",
		Content:   "content-long",
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
