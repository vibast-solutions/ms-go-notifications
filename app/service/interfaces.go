package service

import (
	"context"
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
