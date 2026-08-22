package entity

import (
	"encoding/json"
	"time"
)

// Message event types (message_events.type).
const (
	EventTypeQueued         = "queued"
	EventTypeAttempt        = "attempt"
	EventTypeFailed         = "failed"
	EventTypeSuppressed     = "suppressed"
	EventTypeDelivery       = "delivery"
	EventTypeBounce         = "bounce"
	EventTypeComplaint      = "complaint"
	EventTypeReject         = "reject"
	EventTypeDeliveryDelay  = "delivery_delay"
	EventTypeSend           = "send"
	EventTypeOpen           = "open"
	EventTypeClick          = "click"
	EventTypeRetryRequested = "retry_requested"
	EventTypeOther          = "other"
)

// MessageEvent mirrors the message_events table — the append-only audit trail
// of everything that happened to a message (attempts, provider events).
// ProviderEventID carries the SNS MessageId and dedupes webhook deliveries.
type MessageEvent struct {
	ID              string
	MessageID       string
	RecipientEmail  *string
	Type            string
	ProviderEventID *string
	Payload         json.RawMessage
	OccurredAt      time.Time
	CreatedAt       time.Time
}
