package v1

import (
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// APIKeyResponse describes an API key for the admin endpoints. The secret (hash
// or plaintext) is never included here — the plaintext is returned exactly once
// at creation via CreateAPIKeyResponse.
type APIKeyResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	Domains    []string   `json:"domains"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
}

// APIKeyListResponse is the GET /v1/admin/api-keys payload.
type APIKeyListResponse struct {
	Items []APIKeyResponse `json:"items"`
}

// CreateAPIKeyRequest is the POST /v1/admin/api-keys body. domains are required
// (≥1) when scopes include "send" and must each be in ALLOWED_DOMAINS.
type CreateAPIKeyRequest struct {
	Name    string   `json:"name"`
	Scopes  []string `json:"scopes"`
	Domains []string `json:"domains"`
}

// CreateAPIKeyResponse is the 201 reply to key creation: the key metadata plus
// the plaintext secret, shown only this once.
type CreateAPIKeyResponse struct {
	APIKeyResponse
	Key string `json:"key"`
}

// ReplaceDomainsRequest is the PUT /v1/admin/api-keys/{id}/domains body.
type ReplaceDomainsRequest struct {
	Domains []string `json:"domains"`
}

// NewAPIKeyResponse maps an API key entity to its response shape, never
// exposing the hash. A nil domain list is rendered as [].
func NewAPIKeyResponse(k *entity.APIKey) APIKeyResponse {
	domains := k.Domains
	if domains == nil {
		domains = []string{}
	}
	return APIKeyResponse{
		ID:         k.ID,
		Name:       k.Name,
		Prefix:     k.KeyPrefix,
		Scopes:     k.Scopes,
		Domains:    domains,
		Status:     k.Status,
		CreatedAt:  k.CreatedAt,
		LastUsedAt: k.LastUsedAt,
		RevokedAt:  k.RevokedAt,
	}
}
