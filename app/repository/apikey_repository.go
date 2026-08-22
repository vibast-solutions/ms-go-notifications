package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// APIKeyRepository persists api_keys and their api_key_domains rows.
type APIKeyRepository struct {
	db *sql.DB
}

// NewAPIKeyRepository returns an APIKeyRepository backed by db.
func NewAPIKeyRepository(db *sql.DB) *APIKeyRepository {
	return &APIKeyRepository{db: db}
}

// Create inserts the key and its domain rows in a single transaction.
func (r *APIKeyRepository) Create(ctx context.Context, key *entity.APIKey, domains []string) error {
	scopes, err := json.Marshal(key.Scopes)
	if err != nil {
		return fmt.Errorf("marshaling scopes: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO api_keys (id, name, key_prefix, key_hash, scopes, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		key.ID, key.Name, key.KeyPrefix, key.KeyHash, string(scopes), key.Status, key.CreatedAt)
	if err != nil {
		return fmt.Errorf("inserting api key: %w", err)
	}

	for _, domain := range domains {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO api_key_domains (api_key_id, domain, created_at) VALUES (?, ?, ?)`,
			key.ID, domain, key.CreatedAt)
		if err != nil {
			return fmt.Errorf("inserting api key domain %q: %w", domain, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing tx: %w", err)
	}
	return nil
}

// FindByPrefix loads one key (with its domains) by key_prefix. Returns
// sql.ErrNoRows when no key has that prefix.
func (r *APIKeyRepository) FindByPrefix(ctx context.Context, prefix string) (*entity.APIKey, error) {
	keys, err := r.query(ctx, `WHERE k.key_prefix = ?`, prefix)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, sql.ErrNoRows
	}
	return keys[0], nil
}

// List returns all keys (with domains), oldest first.
func (r *APIKeyRepository) List(ctx context.Context) ([]*entity.APIKey, error) {
	return r.query(ctx, ``)
}

// query runs the shared key+domains join with an optional WHERE clause and
// folds the joined rows into entities.
func (r *APIKeyRepository) query(ctx context.Context, where string, args ...any) ([]*entity.APIKey, error) {
	q := `SELECT k.id, k.name, k.key_prefix, k.key_hash, k.scopes, k.status,
	             k.created_at, k.last_used_at, k.revoked_at, d.domain
	      FROM api_keys k
	      LEFT JOIN api_key_domains d ON d.api_key_id = k.id ` +
		where + ` ORDER BY k.created_at, k.id, d.domain`

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("querying api keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var keys []*entity.APIKey
	byID := map[string]*entity.APIKey{}
	for rows.Next() {
		var (
			k         entity.APIKey
			rawScopes []byte
			domain    sql.NullString
		)
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.KeyHash, &rawScopes,
			&k.Status, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt, &domain); err != nil {
			return nil, fmt.Errorf("scanning api key: %w", err)
		}

		key, seen := byID[k.ID]
		if !seen {
			if err := json.Unmarshal(rawScopes, &k.Scopes); err != nil {
				return nil, fmt.Errorf("unmarshaling scopes of key %s: %w", k.ID, err)
			}
			key = &k
			byID[k.ID] = key
			keys = append(keys, key)
		}
		if domain.Valid {
			key.Domains = append(key.Domains, domain.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating api keys: %w", err)
	}
	return keys, nil
}

// Revoke marks an active key revoked. Returns sql.ErrNoRows when the id does
// not exist or the key is already revoked.
func (r *APIKeyRepository) Revoke(ctx context.Context, id string, at time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE api_keys SET status = ?, revoked_at = ? WHERE id = ? AND status = ?`,
		entity.APIKeyStatusRevoked, at, id, entity.APIKeyStatusActive)
	if err != nil {
		return fmt.Errorf("revoking api key: %w", err)
	}
	return errNoRowsIfZero(res)
}

// ReplaceDomains swaps the key's domain set in a single transaction.
// Returns sql.ErrNoRows when the id does not exist.
func (r *APIKeyRepository) ReplaceDomains(ctx context.Context, id string, domains []string, at time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Guard the id first so a typo'd id fails instead of silently no-opping.
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM api_keys WHERE id = ?`, id).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return sql.ErrNoRows
		}
		return fmt.Errorf("checking api key: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM api_key_domains WHERE api_key_id = ?`, id); err != nil {
		return fmt.Errorf("deleting api key domains: %w", err)
	}
	for _, domain := range domains {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO api_key_domains (api_key_id, domain, created_at) VALUES (?, ?, ?)`,
			id, domain, at)
		if err != nil {
			return fmt.Errorf("inserting api key domain %q: %w", domain, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing tx: %w", err)
	}
	return nil
}

// TouchLastUsed stamps last_used_at. Callers throttle; this always writes.
func (r *APIKeyRepository) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, at, id)
	if err != nil {
		return fmt.Errorf("touching last_used_at: %w", err)
	}
	return nil
}

func errNoRowsIfZero(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
