package queue

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
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

func TestEmailConsumerProcessMessageAcks(t *testing.T) {
	t.Parallel()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()
	if err := client.XGroupCreateMkStream(ctx, StreamName, ConsumerGroup, "0").Err(); err != nil {
		if strings.Contains(err.Error(), "unknown command") {
			t.Skipf("streams not supported by miniredis: %v", err)
		}
		t.Fatalf("XGroupCreateMkStream: %v", err)
	}

	msgID, err := client.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamName,
		Values: map[string]interface{}{
			"request_id": "req-1",
			"recipient":  "a@b.com",
			"subject":    "subj",
			"content":    "content",
		},
	}).Result()
	if err != nil {
		t.Fatalf("XAdd: %v", err)
	}

	streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    ConsumerGroup,
		Consumer: "c1",
		Streams:  []string{StreamName, ">"},
		Count:    1,
	}).Result()
	if err != nil {
		if strings.Contains(err.Error(), "unknown command") {
			t.Skipf("streams not supported by miniredis: %v", err)
		}
		t.Fatalf("XReadGroup: %v", err)
	}
	if len(streams) == 0 || len(streams[0].Messages) == 0 || streams[0].Messages[0].ID != msgID {
		t.Fatalf("expected message %s to be read", msgID)
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectExec("UPDATE email_history").
		WithArgs(entity.EmailStatusProcessing, "req-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE email_history").
		WithArgs("raw", "req-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE email_history").
		WithArgs(entity.EmailStatusSuccess, "req-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	emailService := service.NewEmailService(noopPreparer{}, noopProvider{}, repository.NewEmailHistoryRepository(db), noopLocker{})
	consumer := NewEmailConsumer(client, emailService, "c1")
	consumer.processMessage(ctx, streams[0].Messages[0])

	pending, err := client.XPending(ctx, StreamName, ConsumerGroup).Result()
	if err != nil {
		if strings.Contains(err.Error(), "unknown command") {
			t.Skipf("streams not supported by miniredis: %v", err)
		}
		t.Fatalf("XPending: %v", err)
	}
	if pending.Count != 0 {
		t.Fatalf("expected 0 pending, got %d", pending.Count)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
