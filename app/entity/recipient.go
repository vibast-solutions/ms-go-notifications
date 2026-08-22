package entity

import "time"

// Recipient types.
const (
	RecipientTypeTo  = "to"
	RecipientTypeCc  = "cc"
	RecipientTypeBcc = "bcc"
)

// Recipient statuses.
const (
	RecipientStatusPending    = "pending"
	RecipientStatusSent       = "sent"
	RecipientStatusDelivered  = "delivered"
	RecipientStatusBounced    = "bounced"
	RecipientStatusComplained = "complained"
	RecipientStatusDelayed    = "delayed"
	RecipientStatusSuppressed = "suppressed"
)

// MessageRecipient mirrors the message_recipients table — per-recipient
// delivery state, updated from provider events.
type MessageRecipient struct {
	ID            string
	MessageID     string
	Type          string
	Email         string
	Name          *string
	Status        string
	BounceType    *string
	BounceSubtype *string
	Diagnostic    *string
	LastEventAt   *time.Time
}
