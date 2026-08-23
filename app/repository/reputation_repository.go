package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ReputationRepository computes the trailing-window inputs of the reputation
// circuit breaker (Business Logic §8): how many recipients were sent to and how
// many hard bounces / complaints were recorded since a cut-off. The service
// turns these counts into bounce/complaint rates.
type ReputationRepository struct {
	db *sql.DB
}

// NewReputationRepository returns a ReputationRepository backed by db.
func NewReputationRepository(db *sql.DB) *ReputationRepository {
	return &ReputationRepository{db: db}
}

// CountSentRecipientsSince returns the number of non-suppressed recipients whose
// message was sent at or after since — the denominator of the bounce/complaint
// rates.
func (r *ReputationRepository) CountSentRecipientsSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*)
		   FROM message_recipients r
		   JOIN messages m ON m.id = r.message_id
		  WHERE m.sent_at >= ? AND r.status <> 'suppressed'`, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting sent recipients: %w", err)
	}
	return n, nil
}

// CountHardBouncesAndComplaintsSince returns the number of hard-bounce and
// complaint events recorded at or after since — the numerators of the two
// rates. A hard bounce is a bounce event whose payload carries
// bounce.bounceType = "Permanent"; the payload is the raw SES event stored by
// the webhook, so the permanent flag lives under $.bounce.bounceType (both the
// event-publishing and legacy notification formats nest it there).
func (r *ReputationRepository) CountHardBouncesAndComplaintsSince(ctx context.Context, since time.Time) (hardBounces, complaints int, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT
		    COALESCE(SUM(CASE WHEN type = 'bounce'
		                       AND payload->>'$.bounce.bounceType' = 'Permanent' THEN 1 ELSE 0 END), 0),
		    COALESCE(SUM(CASE WHEN type = 'complaint' THEN 1 ELSE 0 END), 0)
		   FROM message_events
		  WHERE type IN ('bounce', 'complaint') AND occurred_at >= ?`, since).
		Scan(&hardBounces, &complaints)
	if err != nil {
		return 0, 0, fmt.Errorf("counting bounces and complaints: %w", err)
	}
	return hardBounces, complaints, nil
}
