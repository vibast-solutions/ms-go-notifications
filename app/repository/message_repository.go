package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// MessageRepository persists messages and their child rows (recipients,
// attachments, events). The message row doubles as the queue item.
type MessageRepository struct {
	db *sql.DB
}

// NewMessageRepository returns a MessageRepository backed by db.
func NewMessageRepository(db *sql.DB) *MessageRepository {
	return &MessageRepository{db: db}
}

// BeginTx starts a transaction. The accept path owns the transaction so the
// message and all its children are inserted atomically.
func (r *MessageRepository) BeginTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning tx: %w", err)
	}
	return tx, nil
}

// CreateWithChildren inserts the message and its recipients, attachments and
// events inside the given transaction. The caller commits or rolls back; a
// duplicate-key error on (api_key_id, idempotency_key) surfaces unchanged for
// the caller to detect the idempotency race.
func (r *MessageRepository) CreateWithChildren(
	ctx context.Context,
	tx *sql.Tx,
	msg *entity.Message,
	recipients []*entity.MessageRecipient,
	attachments []*entity.MessageAttachment,
	events []*entity.MessageEvent,
) error {
	replyTo, err := jsonColumn(msg.ReplyTo)
	if err != nil {
		return fmt.Errorf("marshaling reply_to: %w", err)
	}
	headers, err := jsonColumn(msg.Headers)
	if err != nil {
		return fmt.Errorf("marshaling headers: %w", err)
	}
	tags, err := jsonColumn(msg.Tags)
	if err != nil {
		return fmt.Errorf("marshaling tags: %w", err)
	}
	var metadata any
	if len(msg.Metadata) > 0 {
		metadata = []byte(msg.Metadata)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO messages
		   (id, api_key_id, channel, status, from_email, from_name, from_domain,
		    reply_to, subject, text_body, html_body, headers, tags, metadata,
		    idempotency_key, attempts, recipient_count, created_at, updated_at,
		    sent_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.APIKeyID, msg.Channel, msg.Status, msg.FromEmail, msg.FromName, msg.FromDomain,
		replyTo, msg.Subject, msg.TextBody, msg.HTMLBody, headers, tags, metadata,
		msg.IdempotencyKey, msg.Attempts, msg.RecipientCount, msg.CreatedAt, msg.UpdatedAt,
		msg.SentAt, msg.FinishedAt)
	if err != nil {
		if isDuplicateKey(err) {
			return ErrDuplicateKey
		}
		return fmt.Errorf("inserting message: %w", err)
	}

	for _, rec := range recipients {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO message_recipients (id, message_id, type, email, name, status)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			rec.ID, rec.MessageID, rec.Type, rec.Email, rec.Name, rec.Status)
		if err != nil {
			return fmt.Errorf("inserting recipient: %w", err)
		}
	}

	for _, att := range attachments {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO message_attachments (id, message_id, filename, content_type, size_bytes, content)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			att.ID, att.MessageID, att.Filename, att.ContentType, att.SizeBytes, att.Content)
		if err != nil {
			return fmt.Errorf("inserting attachment: %w", err)
		}
	}

	for _, ev := range events {
		var payload any
		if len(ev.Payload) > 0 {
			payload = []byte(ev.Payload)
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO message_events
			   (id, message_id, recipient_email, type, provider_event_id, payload, occurred_at, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			ev.ID, ev.MessageID, ev.RecipientEmail, ev.Type, ev.ProviderEventID, payload, ev.OccurredAt, ev.CreatedAt)
		if err != nil {
			return fmt.Errorf("inserting event: %w", err)
		}
	}

	return nil
}

// FindByID loads a message by id. Returns sql.ErrNoRows when absent.
func (r *MessageRepository) FindByID(ctx context.Context, id string) (*entity.Message, error) {
	return scanMessage(r.db.QueryRowContext(ctx, messageSelect+` WHERE id = ?`, id))
}

// FindByIDForAPIKey loads a message owned by apiKeyID. Returns sql.ErrNoRows
// when the id is unknown or belongs to another key (so callers 404 both).
func (r *MessageRepository) FindByIDForAPIKey(ctx context.Context, id, apiKeyID string) (*entity.Message, error) {
	return scanMessage(r.db.QueryRowContext(ctx, messageSelect+` WHERE id = ? AND api_key_id = ?`, id, apiKeyID))
}

// FindByProviderMessageID loads the message carrying the given provider message
// id (the SES message id events reference). Returns sql.ErrNoRows when no
// message matches — the caller logs and drops the event. The newest match wins
// in the unlikely event of a collision.
func (r *MessageRepository) FindByProviderMessageID(ctx context.Context, providerMessageID string) (*entity.Message, error) {
	return scanMessage(r.db.QueryRowContext(ctx,
		messageSelect+` WHERE provider_message_id = ? ORDER BY created_at DESC LIMIT 1`, providerMessageID))
}

// SetRecipientStatus updates a recipient's delivery state from a provider
// event: it sets status and last_event_at, and (re)sets the bounce/diagnostic
// columns to the supplied values (nil clears them). It matches on
// (message_id, email); an event listing an address the message never had simply
// affects no rows.
func (r *MessageRepository) SetRecipientStatus(ctx context.Context, messageID, email, status string,
	bounceType, bounceSubtype, diagnostic *string, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE message_recipients
		    SET status = ?, bounce_type = ?, bounce_subtype = ?, diagnostic = ?, last_event_at = ?
		  WHERE message_id = ? AND email = ?`,
		status, bounceType, bounceSubtype, diagnostic, at, messageID, email)
	if err != nil {
		return fmt.Errorf("updating recipient status: %w", err)
	}
	return nil
}

// SetLastError records a provider-side rejection reason on the message
// (Business Logic §7: Reject sets last_error without changing the status).
func (r *MessageRepository) SetLastError(ctx context.Context, messageID, lastError string, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE messages SET last_error = ?, updated_at = ? WHERE id = ?`,
		lastError, at, messageID)
	if err != nil {
		return fmt.Errorf("updating last_error: %w", err)
	}
	return nil
}

// FindByIdempotency loads the message stored under (apiKeyID, key). Returns
// sql.ErrNoRows when none exists.
func (r *MessageRepository) FindByIdempotency(ctx context.Context, apiKeyID, key string) (*entity.Message, error) {
	return scanMessage(r.db.QueryRowContext(ctx,
		messageSelect+` WHERE api_key_id = ? AND idempotency_key = ?`, apiKeyID, key))
}

// LoadRecipients returns a message's recipients, oldest first.
func (r *MessageRepository) LoadRecipients(ctx context.Context, messageID string) ([]*entity.MessageRecipient, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, message_id, type, email, name, status, bounce_type, bounce_subtype, diagnostic, last_event_at
		 FROM message_recipients WHERE message_id = ? ORDER BY id`, messageID)
	if err != nil {
		return nil, fmt.Errorf("querying recipients: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*entity.MessageRecipient
	for rows.Next() {
		var rec entity.MessageRecipient
		if err := rows.Scan(&rec.ID, &rec.MessageID, &rec.Type, &rec.Email, &rec.Name, &rec.Status,
			&rec.BounceType, &rec.BounceSubtype, &rec.Diagnostic, &rec.LastEventAt); err != nil {
			return nil, fmt.Errorf("scanning recipient: %w", err)
		}
		out = append(out, &rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating recipients: %w", err)
	}
	return out, nil
}

// LoadEvents returns a message's events in chronological order.
func (r *MessageRepository) LoadEvents(ctx context.Context, messageID string) ([]*entity.MessageEvent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, message_id, recipient_email, type, provider_event_id, payload, occurred_at, created_at
		 FROM message_events WHERE message_id = ? ORDER BY occurred_at, id`, messageID)
	if err != nil {
		return nil, fmt.Errorf("querying events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*entity.MessageEvent
	for rows.Next() {
		var (
			ev      entity.MessageEvent
			payload []byte
		)
		if err := rows.Scan(&ev.ID, &ev.MessageID, &ev.RecipientEmail, &ev.Type,
			&ev.ProviderEventID, &payload, &ev.OccurredAt, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		if payload != nil {
			ev.Payload = json.RawMessage(payload)
		}
		out = append(out, &ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating events: %w", err)
	}
	return out, nil
}

// messageSelect lists every messages column in a stable order shared by all
// single-row loaders and scanMessage.
const messageSelect = `SELECT
	id, api_key_id, channel, status, provider, provider_message_id,
	from_email, from_name, from_domain, reply_to, subject, text_body, html_body,
	headers, tags, metadata, idempotency_key, attempts, last_error,
	next_attempt_at, locked_at, locked_by, recipient_count,
	created_at, updated_at, sent_at, finished_at
FROM messages`

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanMessage folds one messageSelect row into an entity, decoding the JSON
// columns. It maps sql.ErrNoRows through unchanged.
func scanMessage(row rowScanner) (*entity.Message, error) {
	var (
		msg                          entity.Message
		replyTo, headers, tags, meta []byte
	)
	err := row.Scan(
		&msg.ID, &msg.APIKeyID, &msg.Channel, &msg.Status, &msg.Provider, &msg.ProviderMessageID,
		&msg.FromEmail, &msg.FromName, &msg.FromDomain, &replyTo, &msg.Subject, &msg.TextBody, &msg.HTMLBody,
		&headers, &tags, &meta, &msg.IdempotencyKey, &msg.Attempts, &msg.LastError,
		&msg.NextAttemptAt, &msg.LockedAt, &msg.LockedBy, &msg.RecipientCount,
		&msg.CreatedAt, &msg.UpdatedAt, &msg.SentAt, &msg.FinishedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("scanning message: %w", err)
	}

	if replyTo != nil {
		if err := json.Unmarshal(replyTo, &msg.ReplyTo); err != nil {
			return nil, fmt.Errorf("unmarshaling reply_to: %w", err)
		}
	}
	if headers != nil {
		if err := json.Unmarshal(headers, &msg.Headers); err != nil {
			return nil, fmt.Errorf("unmarshaling headers: %w", err)
		}
	}
	if tags != nil {
		if err := json.Unmarshal(tags, &msg.Tags); err != nil {
			return nil, fmt.Errorf("unmarshaling tags: %w", err)
		}
	}
	if meta != nil {
		msg.Metadata = json.RawMessage(meta)
	}
	return &msg, nil
}

// jsonColumn marshals a map/slice to a JSON column value, returning nil (SQL
// NULL) for empty input so unset optional columns stay NULL.
func jsonColumn(v any) (any, error) {
	switch t := v.(type) {
	case map[string]string:
		if len(t) == 0 {
			return nil, nil
		}
	case []entity.EmailAddress:
		if len(t) == 0 {
			return nil, nil
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}
