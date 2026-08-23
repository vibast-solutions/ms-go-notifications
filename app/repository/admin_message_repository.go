package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// MessageFilter carries the WHERE criteria of the admin message history query
// (GET /v1/admin/messages). Zero-value fields are ignored. The cursor fields
// (BeforeCreatedAt/BeforeID) implement keyset pagination on (created_at, id)
// descending: they are set from the previous page's last row and stay stable
// under concurrent inserts.
type MessageFilter struct {
	Status      string     // exact match on messages.status
	FromDomain  string     // exact match on from_domain
	From        string     // from_email contains (LIKE %x%)
	To          string     // recipient email contains (LIKE %x%), any type
	Subject     string     // subject contains (LIKE %x%)
	APIKeyID    string     // exact match on api_key_id
	TagPath     string     // JSON path for the tag filter, e.g. $."app" (empty = no tag filter)
	TagValue    string     // required tag value when TagPath is set
	CreatedFrom *time.Time // created_at >= (inclusive)
	CreatedTo   *time.Time // created_at <= (inclusive)

	BeforeCreatedAt *time.Time // keyset cursor: created_at of the previous page's last row
	BeforeID        string     // keyset cursor: id of the previous page's last row
}

// MessageSummaryRow is one row of the admin history list: the summary columns
// only (never bodies), plus the joined API key name and the first "to"
// recipient.
type MessageSummaryRow struct {
	ID             string
	Status         string
	FromEmail      string
	FromName       *string
	Subject        string
	RecipientCount int
	FirstTo        *string
	APIKeyName     string
	Provider       *string
	Attempts       int
	CreatedAt      time.Time
	SentAt         *time.Time
}

// ListForAdmin returns up to limit message summaries matching filter, newest
// first (created_at desc, id desc). The caller typically asks for limit+1 rows
// to detect whether a further page exists.
func (r *MessageRepository) ListForAdmin(ctx context.Context, f MessageFilter, limit int) ([]*MessageSummaryRow, error) {
	where, args := f.build()

	q := `SELECT m.id, m.status, m.from_email, m.from_name, m.subject, m.recipient_count,
	             (SELECT r.email FROM message_recipients r
	                WHERE r.message_id = m.id AND r.type = 'to' ORDER BY r.id LIMIT 1) AS first_to,
	             COALESCE(k.name, '') AS api_key_name,
	             m.provider, m.attempts, m.created_at, m.sent_at
	        FROM messages m
	        LEFT JOIN api_keys k ON k.id = m.api_key_id ` +
		where + ` ORDER BY m.created_at DESC, m.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("querying admin messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*MessageSummaryRow
	for rows.Next() {
		var s MessageSummaryRow
		if err := rows.Scan(&s.ID, &s.Status, &s.FromEmail, &s.FromName, &s.Subject,
			&s.RecipientCount, &s.FirstTo, &s.APIKeyName, &s.Provider, &s.Attempts,
			&s.CreatedAt, &s.SentAt); err != nil {
			return nil, fmt.Errorf("scanning admin message: %w", err)
		}
		out = append(out, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating admin messages: %w", err)
	}
	return out, nil
}

// build renders the WHERE clause and its arguments from the filter. It is kept
// separate so it can be unit-tested and reused.
func (f MessageFilter) build() (string, []any) {
	var conds []string
	var args []any

	if f.Status != "" {
		conds = append(conds, "m.status = ?")
		args = append(args, f.Status)
	}
	if f.FromDomain != "" {
		conds = append(conds, "m.from_domain = ?")
		args = append(args, f.FromDomain)
	}
	if f.From != "" {
		conds = append(conds, "m.from_email LIKE ?")
		args = append(args, "%"+f.From+"%")
	}
	if f.Subject != "" {
		conds = append(conds, "m.subject LIKE ?")
		args = append(args, "%"+f.Subject+"%")
	}
	if f.APIKeyID != "" {
		conds = append(conds, "m.api_key_id = ?")
		args = append(args, f.APIKeyID)
	}
	if f.To != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM message_recipients r WHERE r.message_id = m.id AND r.email LIKE ?)")
		args = append(args, "%"+f.To+"%")
	}
	if f.TagPath != "" {
		conds = append(conds, "JSON_UNQUOTE(JSON_EXTRACT(m.tags, ?)) = ?")
		args = append(args, f.TagPath, f.TagValue)
	}
	if f.CreatedFrom != nil {
		conds = append(conds, "m.created_at >= ?")
		args = append(args, *f.CreatedFrom)
	}
	if f.CreatedTo != nil {
		conds = append(conds, "m.created_at <= ?")
		args = append(args, *f.CreatedTo)
	}
	if f.BeforeCreatedAt != nil {
		conds = append(conds, "(m.created_at < ? OR (m.created_at = ? AND m.id < ?))")
		args = append(args, *f.BeforeCreatedAt, *f.BeforeCreatedAt, f.BeforeID)
	}

	if len(conds) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

// LoadAttachmentsMeta returns a message's attachment metadata (no content), for
// the admin detail view. purged is derived by the caller from PurgedAt.
func (r *MessageRepository) LoadAttachmentsMeta(ctx context.Context, messageID string) ([]*entity.MessageAttachment, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, message_id, filename, content_type, size_bytes, purged_at
		   FROM message_attachments WHERE message_id = ? ORDER BY id`, messageID)
	if err != nil {
		return nil, fmt.Errorf("querying attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*entity.MessageAttachment
	for rows.Next() {
		var a entity.MessageAttachment
		if err := rows.Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.PurgedAt); err != nil {
			return nil, fmt.Errorf("scanning attachment: %w", err)
		}
		out = append(out, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating attachments: %w", err)
	}
	return out, nil
}

// FindAttachment loads a single attachment (with content) scoped to its
// message. Returns sql.ErrNoRows when the pair does not exist. A purged
// attachment still resolves (content NULL, purged_at set) so the caller can
// answer 410 rather than 404.
func (r *MessageRepository) FindAttachment(ctx context.Context, messageID, attachmentID string) (*entity.MessageAttachment, error) {
	var a entity.MessageAttachment
	err := r.db.QueryRowContext(ctx,
		`SELECT id, message_id, filename, content_type, size_bytes, content, purged_at
		   FROM message_attachments WHERE id = ? AND message_id = ?`, attachmentID, messageID).
		Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.Content, &a.PurgedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("querying attachment: %w", err)
	}
	return &a, nil
}

// RequeueForRetry re-queues a failed message and appends the retry event in one
// transaction. The UPDATE is guarded by status='failed' so a message that moved
// on (or never was failed) is not disturbed; it reports whether the row was
// re-queued (false = the guard did not match, i.e. not currently failed).
func (r *MessageRepository) RequeueForRetry(ctx context.Context, id string, ev *entity.MessageEvent, at time.Time) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("beginning tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`UPDATE messages
		    SET status = 'queued', next_attempt_at = NULL, locked_at = NULL, locked_by = NULL,
		        finished_at = NULL, updated_at = ?
		  WHERE id = ? AND status = 'failed'`, at, id)
	if err != nil {
		return false, fmt.Errorf("re-queuing message: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("reading rows affected: %w", err)
	}
	if n == 0 {
		return false, nil
	}

	var payload any
	if len(ev.Payload) > 0 {
		payload = []byte(ev.Payload)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO message_events
		   (id, message_id, recipient_email, type, provider_event_id, payload, occurred_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.ID, ev.MessageID, ev.RecipientEmail, ev.Type, ev.ProviderEventID, payload, ev.OccurredAt, ev.CreatedAt)
	if err != nil {
		return false, fmt.Errorf("inserting retry event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing tx: %w", err)
	}
	return true, nil
}

// CountMessagesByStatus returns the row count per status for the given
// statuses (backlog gauges). Statuses with no rows are absent from the map.
func (r *MessageRepository) CountMessagesByStatus(ctx context.Context, statuses []string) (map[string]int, error) {
	if len(statuses) == 0 {
		return map[string]int{}, nil
	}
	placeholders := make([]string, len(statuses))
	args := make([]any, len(statuses))
	for i, s := range statuses {
		placeholders[i] = "?"
		args[i] = s
	}
	q := `SELECT status, COUNT(*) FROM messages WHERE status IN (` +
		strings.Join(placeholders, ",") + `) GROUP BY status`

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("counting messages by status: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]int, len(statuses))
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scanning status count: %w", err)
		}
		out[status] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating status counts: %w", err)
	}
	return out, nil
}

// StatusBucketRow is one (time bucket, status) count from the messages stats
// aggregation. Bucket is the RFC3339 UTC start of the bucket.
type StatusBucketRow struct {
	Bucket string
	Status string
	Count  int
}

// EventBucketRow is one time bucket of recipient-outcome counts derived from
// message_events. Hard vs soft bounce is split on the SES bounceType.
type EventBucketRow struct {
	Bucket      string
	Delivered   int
	BouncedHard int
	BouncedSoft int
	Complained  int
}

// bucketFormat maps a granularity to the DATE_FORMAT template producing an
// RFC3339 UTC bucket key (timestamps are stored UTC).
func bucketFormat(granularity string) string {
	if granularity == "hour" {
		return "%Y-%m-%dT%H:00:00Z"
	}
	return "%Y-%m-%dT00:00:00Z"
}

// StatsMessageBuckets aggregates accepted/sent/failed/suppressed per time
// bucket: it groups messages by their created_at bucket and current status
// within [from, to). An optional fromDomain narrows the set. Buckets with no
// messages are simply absent (the service fills the gaps).
func (r *MessageRepository) StatsMessageBuckets(ctx context.Context, from, to time.Time, fromDomain, granularity string) ([]StatusBucketRow, error) {
	bucket := "DATE_FORMAT(created_at, '" + bucketFormat(granularity) + "')"
	conds := []string{"created_at >= ?", "created_at < ?"}
	args := []any{from, to}
	if fromDomain != "" {
		conds = append(conds, "from_domain = ?")
		args = append(args, fromDomain)
	}

	q := `SELECT ` + bucket + ` AS bucket, status, COUNT(*)
	        FROM messages WHERE ` + strings.Join(conds, " AND ") + `
	       GROUP BY bucket, status`

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregating message stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []StatusBucketRow
	for rows.Next() {
		var row StatusBucketRow
		if err := rows.Scan(&row.Bucket, &row.Status, &row.Count); err != nil {
			return nil, fmt.Errorf("scanning message stats: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating message stats: %w", err)
	}
	return out, nil
}

// StatsEventBuckets aggregates delivered/bounced(hard,soft)/complained per time
// bucket from message_events, bucketed by the event's occurred_at within
// [from, to). A hard bounce is a bounce whose payload carries
// bounce.bounceType = "Permanent" (the same path the reputation breaker uses).
// An optional fromDomain joins back to messages.
func (r *MessageRepository) StatsEventBuckets(ctx context.Context, from, to time.Time, fromDomain, granularity string) ([]EventBucketRow, error) {
	bucket := "DATE_FORMAT(e.occurred_at, '" + bucketFormat(granularity) + "')"
	conds := []string{"e.type IN ('delivery','bounce','complaint')", "e.occurred_at >= ?", "e.occurred_at < ?"}
	args := []any{from, to}

	join := ""
	if fromDomain != "" {
		join = "JOIN messages m ON m.id = e.message_id "
		conds = append(conds, "m.from_domain = ?")
		args = append(args, fromDomain)
	}

	q := `SELECT ` + bucket + ` AS bucket,
	             COALESCE(SUM(e.type = 'delivery'), 0) AS delivered,
	             COALESCE(SUM(e.type = 'bounce' AND e.payload->>'$.bounce.bounceType' = 'Permanent'), 0) AS bounced_hard,
	             COALESCE(SUM(e.type = 'bounce' AND (e.payload->>'$.bounce.bounceType' IS NULL OR e.payload->>'$.bounce.bounceType' <> 'Permanent')), 0) AS bounced_soft,
	             COALESCE(SUM(e.type = 'complaint'), 0) AS complained
	        FROM message_events e ` + join + `
	       WHERE ` + strings.Join(conds, " AND ") + `
	       GROUP BY bucket`

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregating event stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []EventBucketRow
	for rows.Next() {
		var row EventBucketRow
		if err := rows.Scan(&row.Bucket, &row.Delivered, &row.BouncedHard, &row.BouncedSoft, &row.Complained); err != nil {
			return nil, fmt.Errorf("scanning event stats: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating event stats: %w", err)
	}
	return out, nil
}
