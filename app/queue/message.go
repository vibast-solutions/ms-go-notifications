package queue

import "context"

const StreamName = "notifications:email:send-raw"
const ConsumerGroup = "email-consumers"

// EmailPublisher abstracts message publishing to the email stream.
type EmailPublisher interface {
	Publish(ctx context.Context, msg EmailMessage) error
}

type EmailMessage struct {
	RequestID string
	Recipient string
	Subject   string
	Content   string
}
