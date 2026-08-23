package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// SettingsRepository reads the settings table — small runtime state such as the
// reputation circuit breaker's pause flag.
type SettingsRepository struct {
	db *sql.DB
}

// NewSettingsRepository returns a SettingsRepository backed by db.
func NewSettingsRepository(db *sql.DB) *SettingsRepository {
	return &SettingsRepository{db: db}
}

// SendingPaused reports whether global sending is paused. The pause is
// represented by the presence of the settings.sending_paused row (written by
// the circuit breaker or an admin in later stories); this story only honours
// it. Resume removes the row.
func (r *SettingsRepository) SendingPaused(ctx context.Context) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx,
		"SELECT 1 FROM settings WHERE `key` = ? LIMIT 1", entity.SettingSendingPaused).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("reading sending_paused: %w", err)
	default:
		return true, nil
	}
}
