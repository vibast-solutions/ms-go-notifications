package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// SuppressionFilter carries the WHERE criteria of the admin suppression list
// (GET /v1/admin/suppressions). The cursor fields implement keyset pagination
// on (created_at, email) descending.
type SuppressionFilter struct {
	Email  string // email contains (LIKE %x%)
	Reason string // exact match on reason

	BeforeCreatedAt *time.Time // keyset cursor: created_at of the previous page's last row
	BeforeEmail     string     // keyset cursor: email of the previous page's last row
}

// ListForAdmin returns up to limit suppressions matching filter, newest first
// (created_at desc, email desc). The caller asks for limit+1 to detect a
// further page.
func (r *SuppressionRepository) ListForAdmin(ctx context.Context, f SuppressionFilter, limit int) ([]*entity.Suppression, error) {
	var conds []string
	var args []any
	if f.Email != "" {
		conds = append(conds, "email LIKE ?")
		args = append(args, "%"+f.Email+"%")
	}
	if f.Reason != "" {
		conds = append(conds, "reason = ?")
		args = append(args, f.Reason)
	}
	if f.BeforeCreatedAt != nil {
		conds = append(conds, "(created_at < ? OR (created_at = ? AND email < ?))")
		args = append(args, *f.BeforeCreatedAt, *f.BeforeCreatedAt, f.BeforeEmail)
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ") + " "
	}
	q := `SELECT email, reason, source_message_id, note, created_at, expires_at
	        FROM suppressions ` + where + `ORDER BY created_at DESC, email DESC LIMIT ?`
	args = append(args, limit)

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

// Insert adds a suppression, failing with ErrDuplicateKey when the address is
// already suppressed. Unlike Upsert (used by the idempotent event path), the
// admin add must report the conflict so the endpoint answers 409.
func (r *SuppressionRepository) Insert(ctx context.Context, s *entity.Suppression) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO suppressions (email, reason, source_message_id, note, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		s.Email, s.Reason, s.SourceMessageID, s.Note, s.CreatedAt, s.ExpiresAt)
	if err != nil {
		if isDuplicateKey(err) {
			return ErrDuplicateKey
		}
		return fmt.Errorf("inserting suppression: %w", err)
	}
	return nil
}

// Delete removes a suppression by email. Returns sql.ErrNoRows when no row
// matched, so the endpoint can answer 404.
func (r *SuppressionRepository) Delete(ctx context.Context, email string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM suppressions WHERE email = ?`, email)
	if err != nil {
		return fmt.Errorf("deleting suppression: %w", err)
	}
	return errNoRowsIfZero(res)
}
