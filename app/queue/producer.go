package queue

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type EmailProducer struct {
	client *redis.Client
}

// NewEmailProducer constructs a Redis stream producer.
func NewEmailProducer(client *redis.Client) *EmailProducer {
	return &EmailProducer{client: client}
}

// Publish pushes an email message onto the stream.
func (p *EmailProducer) Publish(ctx context.Context, msg EmailMessage) error {
	_, err := p.client.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamName,
		Values: map[string]interface{}{
			"request_id": msg.RequestID,
			"recipient":  msg.Recipient,
			"subject":    msg.Subject,
			"content":    msg.Content,
		},
	}).Result()
	if err != nil {
		return fmt.Errorf("xadd to %s: %w", StreamName, err)
	}
	return nil
}
