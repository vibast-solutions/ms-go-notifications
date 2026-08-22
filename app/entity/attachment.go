package entity

import "time"

// MessageAttachment mirrors the message_attachments table. Content becomes
// NULL once purged by `cleanup --target attachments`; PurgedAt records when.
type MessageAttachment struct {
	ID          string
	MessageID   string
	Filename    string
	ContentType string
	SizeBytes   int
	Content     []byte
	PurgedAt    *time.Time
}
