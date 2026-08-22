package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// EventRepository appends rows to message_events. The accept path inserts its
// queued/suppressed event inside the create transaction; this standalone
// inserter serves the processor and webhook stories.
type EventRepository struct {
	db *sql.DB
}

// NewEventRepository returns an EventRepository backed by db.
func NewEventRepository(db *sql.DB) *EventRepository {
	return &EventRepository{db: db}
}

// Insert appends one event. A nil Payload is stored as SQL NULL.
func (r *EventRepository) Insert(ctx context.Context, ev *entity.MessageEvent) error {
	var payload any
	if len(ev.Payload) > 0 {
		payload = []byte(ev.Payload)
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO message_events
		   (id, message_id, recipient_email, type, provider_event_id, payload, occurred_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.ID, ev.MessageID, ev.RecipientEmail, ev.Type, ev.ProviderEventID, payload, ev.OccurredAt, ev.CreatedAt)
	if err != nil {
		return fmt.Errorf("inserting event: %w", err)
	}
	return nil
}
