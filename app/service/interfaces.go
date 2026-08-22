package service

import (
	"context"
	"database/sql"
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// APIKeyRepository is the persistence contract the API key service depends
// on. Implemented by repository.APIKeyRepository; missing rows are reported
// as sql.ErrNoRows.
type APIKeyRepository interface {
	Create(ctx context.Context, key *entity.APIKey, domains []string) error
	FindByPrefix(ctx context.Context, prefix string) (*entity.APIKey, error)
	List(ctx context.Context) ([]*entity.APIKey, error)
	Revoke(ctx context.Context, id string, at time.Time) error
	ReplaceDomains(ctx context.Context, id string, domains []string, at time.Time) error
	TouchLastUsed(ctx context.Context, id string, at time.Time) error
}

// APIKeys is the API key business logic contract consumed by the auth
// middleware, the apikey CLI, and (later) the admin endpoints.
type APIKeys interface {
	// Generate creates a key and returns its plaintext exactly once.
	Generate(ctx context.Context, name string, scopes, domains []string) (string, *entity.APIKey, error)
	// Authenticate resolves a plaintext bearer token to its key (with
	// domains). Returns ErrInvalidAPIKey or ErrAPIKeyRevoked on failure.
	Authenticate(ctx context.Context, plaintext string) (*entity.APIKey, error)
	List(ctx context.Context) ([]*entity.APIKey, error)
	Revoke(ctx context.Context, id string) error
	ReplaceDomains(ctx context.Context, id string, domains []string) error
	// TouchLastUsed stamps last_used_at now. Callers throttle the frequency.
	TouchLastUsed(ctx context.Context, id string) error
}

// MessageRepository is the persistence contract for messages and their child
// rows. Implemented by repository.MessageRepository. The accept path owns the
// transaction: BeginTx starts it, CreateWithChildren writes into it, and the
// service commits. A unique-constraint violation surfaces as
// repository.ErrDuplicateKey.
type MessageRepository interface {
	BeginTx(ctx context.Context) (*sql.Tx, error)
	CreateWithChildren(ctx context.Context, tx *sql.Tx, msg *entity.Message,
		recipients []*entity.MessageRecipient, attachments []*entity.MessageAttachment,
		events []*entity.MessageEvent) error
	FindByIdempotency(ctx context.Context, apiKeyID, key string) (*entity.Message, error)
	FindByIDForAPIKey(ctx context.Context, id, apiKeyID string) (*entity.Message, error)
	LoadRecipients(ctx context.Context, messageID string) ([]*entity.MessageRecipient, error)
	LoadEvents(ctx context.Context, messageID string) ([]*entity.MessageEvent, error)
}

// SuppressionRepository is the accept-path suppression lookup. Implemented by
// repository.SuppressionRepository.
type SuppressionRepository interface {
	FindByEmails(ctx context.Context, emails []string) ([]*entity.Suppression, error)
}

// Dispatcher hands a committed, queued message id to the in-process worker
// pool. Implemented by internal/worker (NoopDispatcher until VIB-9 provides the
// real pool). Declared here so the accept path depends only on the behaviour,
// not on the worker package.
type Dispatcher interface {
	Enqueue(id string)
}
