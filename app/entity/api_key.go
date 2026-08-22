package entity

import "time"

// API key statuses.
const (
	APIKeyStatusActive  = "active"
	APIKeyStatusRevoked = "revoked"
)

// API key scopes.
const (
	ScopeSend  = "send"
	ScopeAdmin = "admin"
)

// APIKey mirrors the api_keys table. Only the sha256 hash of the plaintext
// key is stored; key_prefix is the short lookup fragment from ntf_<prefix>_….
type APIKey struct {
	ID         string
	Name       string
	KeyPrefix  string
	KeyHash    string
	Scopes     []string
	Status     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// APIKeyDomain mirrors the api_key_domains table: one row per sender domain a
// key may use. No wildcards; domains are stored lower-case.
type APIKeyDomain struct {
	APIKeyID  string
	Domain    string
	CreatedAt time.Time
}
