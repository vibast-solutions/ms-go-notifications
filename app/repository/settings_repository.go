package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// SettingsRepository reads and writes the settings table — small runtime state
// such as the reputation circuit breaker's pause flag. The presence of the
// sending_paused row means sending is paused; its JSON value carries the
// reason, since and by fields. Resume deletes the row.
type SettingsRepository struct {
	db *sql.DB
}

// NewSettingsRepository returns a SettingsRepository backed by db.
func NewSettingsRepository(db *sql.DB) *SettingsRepository {
	return &SettingsRepository{db: db}
}

// Get returns the setting stored under key. A missing row surfaces as
// sql.ErrNoRows (unwrapped) so callers can detect absence with errors.Is.
func (r *SettingsRepository) Get(ctx context.Context, key string) (*entity.Setting, error) {
	var (
		s     entity.Setting
		value []byte
	)
	err := r.db.QueryRowContext(ctx,
		"SELECT `key`, value, updated_at FROM settings WHERE `key` = ?", key).
		Scan(&s.Key, &value, &s.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("reading setting %q: %w", key, err)
	}
	s.Value = value
	return &s, nil
}

// Upsert writes value under key, creating the row or replacing an existing
// value/updated_at. It is idempotent — the reputation service uses it to set the
// pause flag whether or not one is already present.
func (r *SettingsRepository) Upsert(ctx context.Context, key string, value json.RawMessage, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		"INSERT INTO settings (`key`, value, updated_at) VALUES (?, ?, ?) "+
			"ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = VALUES(updated_at)",
		key, []byte(value), now)
	if err != nil {
		return fmt.Errorf("upserting setting %q: %w", key, err)
	}
	return nil
}

// Delete removes the row stored under key. Deleting a missing key is not an
// error (resuming an already-running service is a no-op).
func (r *SettingsRepository) Delete(ctx context.Context, key string) error {
	if _, err := r.db.ExecContext(ctx, "DELETE FROM settings WHERE `key` = ?", key); err != nil {
		return fmt.Errorf("deleting setting %q: %w", key, err)
	}
	return nil
}
