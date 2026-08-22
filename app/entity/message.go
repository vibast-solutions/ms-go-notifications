package entity

import (
	"encoding/json"
	"time"
)

// Message channels.
const (
	ChannelEmail = "email"
)

// Message statuses.
const (
	MessageStatusQueued     = "queued"
	MessageStatusSending    = "sending"
	MessageStatusSent       = "sent"
	MessageStatusFailed     = "failed"
	MessageStatusSuppressed = "suppressed"
)

// EmailAddress is a name/address pair as stored in JSON columns (reply_to).
type EmailAddress struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// Message mirrors the messages table — one row per accepted send request.
// The row doubles as the queue item: status/next_attempt_at/locked_* drive
// the claim-based processing loop.
type Message struct {
	ID                string
	APIKeyID          string
	Channel           string
	Status            string
	Provider          *string
	ProviderMessageID *string
	FromEmail         string
	FromName          *string
	FromDomain        string
	ReplyTo           []EmailAddress
	Subject           string
	TextBody          *string
	HTMLBody          *string
	Headers           map[string]string
	Tags              []string
	Metadata          json.RawMessage
	IdempotencyKey    *string
	Attempts          int
	LastError         *string
	NextAttemptAt     *time.Time
	LockedAt          *time.Time
	LockedBy          *string
	RecipientCount    int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	SentAt            *time.Time
	FinishedAt        *time.Time
}
