package queue

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestEmailProducerPublish(t *testing.T) {
	t.Parallel()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	producer := NewEmailProducer(client)
	if err := producer.Publish(context.Background(), EmailMessage{
		RequestID: "req-1",
		Recipient: "a@b.com",
		Subject:   "subj",
		Content:   "content",
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if got := client.XLen(context.Background(), StreamName).Val(); got != 1 {
		t.Fatalf("expected 1 message, got %d", got)
	}
}
