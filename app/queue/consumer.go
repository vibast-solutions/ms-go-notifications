package queue

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

type EmailConsumer struct {
	client       *redis.Client
	emailService *service.EmailService
	consumerName string
}

// NewEmailConsumer constructs a Redis stream consumer.
func NewEmailConsumer(client *redis.Client, emailService *service.EmailService, consumerName string) *EmailConsumer {
	return &EmailConsumer{
		client:       client,
		emailService: emailService,
		consumerName: consumerName,
	}
}

// Run starts the consumer loop and blocks until context cancellation.
func (c *EmailConsumer) Run(ctx context.Context) error {
	if err := c.ensureGroup(ctx); err != nil {
		return err
	}

	log.Printf("Consumer %s started on stream %s", c.consumerName, StreamName)

	// First drain pending messages, then switch to reading new ones.
	startID := "0"
	for {
		select {
		case <-ctx.Done():
			log.Println("Consumer shutting down")
			return nil
		default:
		}

		streams, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    ConsumerGroup,
			Consumer: c.consumerName,
			Streams:  []string{StreamName, startID},
			Count:    1,
			Block:    5 * time.Second,
		}).Result()
		if err != nil {
			if err == redis.Nil {
				// No messages available within block timeout.
				if startID == "0" {
					// Finished draining pending messages, switch to new.
					startID = ">"
				}
				continue
			}
			if ctx.Err() != nil {
				log.Println("Consumer shutting down")
				return nil
			}
			log.Printf("XReadGroup error: %v", err)
			time.Sleep(time.Second)
			continue
		}

		for _, stream := range streams {
			if len(stream.Messages) == 0 && startID == "0" {
				// No more pending messages, switch to reading new.
				startID = ">"
				continue
			}
			for _, msg := range stream.Messages {
				c.processMessage(ctx, msg)
			}
		}
	}
}

// processMessage handles a single message and acks on success.
func (c *EmailConsumer) processMessage(ctx context.Context, msg redis.XMessage) {
	requestID, _ := msg.Values["request_id"].(string)
	recipient, _ := msg.Values["recipient"].(string)
	subject, _ := msg.Values["subject"].(string)
	content, _ := msg.Values["content"].(string)

	log.Printf("Processing message %s (request_id=%s recipient=%s)", msg.ID, requestID, recipient)

	sendCtx := service.WithRequestID(ctx, requestID)
	sendCtx, cancel := context.WithTimeout(sendCtx, 30*time.Second)
	defer cancel()

	if err := c.emailService.SendRaw(sendCtx, recipient, subject, content); err != nil {
		log.Printf("SendRaw failed (request_id=%s): %v — message %s stays pending", requestID, err, msg.ID)
		return
	}

	if err := c.client.XAck(ctx, StreamName, ConsumerGroup, msg.ID).Err(); err != nil {
		log.Printf("XAck failed for message %s: %v", msg.ID, err)
	}
}

// ensureGroup creates the stream and consumer group if missing.
func (c *EmailConsumer) ensureGroup(ctx context.Context) error {
	err := c.client.XGroupCreateMkStream(ctx, StreamName, ConsumerGroup, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return err
	}
	return nil
}
