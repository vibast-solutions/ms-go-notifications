package worker

import (
	"context"
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// MessageStore is the persistence the worker needs. It is satisfied by
// *repository.MessageRepository; declaring it here (consumer-side) keeps the
// worker decoupled from the concrete type and lets tests inject an in-memory
// store that enforces the claim guard.
type MessageStore interface {
	// Claim atomically takes ownership of a queued, due message; true means
	// this process won the row (see repository.MessageRepository.Claim).
	Claim(ctx context.Context, id, lockedBy string, now time.Time) (bool, error)
	FindByID(ctx context.Context, id string) (*entity.Message, error)
	LoadRecipients(ctx context.Context, messageID string) ([]*entity.MessageRecipient, error)
	LoadAttachments(ctx context.Context, messageID string) ([]*entity.MessageAttachment, error)
	MarkSent(ctx context.Context, id, providerName, providerMessageID string, now time.Time) error
	MarkRetry(ctx context.Context, id, providerName, lastErr string, nextAttemptAt, now time.Time) error
	MarkFailed(ctx context.Context, id, providerName, lastErr string, now time.Time) error
	UpdateRecipientsStatus(ctx context.Context, messageID, fromStatus, toStatus string) (int64, error)
	FindDueQueued(ctx context.Context, now time.Time, limit int, excludeIDs []string) ([]string, error)
	UnstickStaleSending(ctx context.Context, olderThan, now time.Time) (int64, error)
}

// EventInserter appends message events. Satisfied by *repository.EventRepository.
type EventInserter interface {
	Insert(ctx context.Context, ev *entity.MessageEvent) error
}

// SettingsReader reports whether global sending is paused. Satisfied by
// *repository.SettingsRepository.
type SettingsReader interface {
	SendingPaused(ctx context.Context) (bool, error)
}

// Enqueuer offers a message id to the worker pool. Satisfied by *Dispatcher.
// The processor uses it to arm the retry fast-path.
type Enqueuer interface {
	Enqueue(id string) bool
}
