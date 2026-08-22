package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// SuppressionRepository reads and writes the suppressions table.
type SuppressionRepository struct {
	db *sql.DB
}

// NewSuppressionRepository returns a SuppressionRepository backed by db.
func NewSuppressionRepository(db *sql.DB) *SuppressionRepository {
	return &SuppressionRepository{db: db}
}

// FindByEmails returns the suppressions matching any of emails. Lookup is
// exact on the stored (lower-cased) address; callers normalize first. An empty
// input returns no rows without hitting the database.
func (r *SuppressionRepository) FindByEmails(ctx context.Context, emails []string) ([]*entity.Suppression, error) {
	if len(emails) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(emails))
	args := make([]any, len(emails))
	for i, e := range emails {
		placeholders[i] = "?"
		args[i] = e
	}

	q := `SELECT email, reason, source_message_id, note, created_at, expires_at
	      FROM suppressions WHERE email IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("querying suppressions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*entity.Suppression
	for rows.Next() {
		var s entity.Suppression
		if err := rows.Scan(&s.Email, &s.Reason, &s.SourceMessageID, &s.Note, &s.CreatedAt, &s.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scanning suppression: %w", err)
		}
		out = append(out, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating suppressions: %w", err)
	}
	return out, nil
}
