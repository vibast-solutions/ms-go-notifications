package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// Claim attempts to atomically take ownership of a queued, due message for
// sending. It is the lock: the UPDATE succeeds only if the row is still
// 'queued' and due, so with several instances exactly one claim wins. It
// returns true when this process owns the message (exactly one row changed),
// false when another worker already has it or it is no longer due. attempts is
// incremented as part of the claim.
func (r *MessageRepository) Claim(ctx context.Context, id, lockedBy string, now time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE messages
		    SET status='sending', locked_at=?, locked_by=?, attempts=attempts+1, updated_at=?
		  WHERE id=? AND status='queued' AND (next_attempt_at IS NULL OR next_attempt_at <= ?)`,
		now, lockedBy, now, id, now)
	if err != nil {
		return false, fmt.Errorf("claiming message: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim rows affected: %w", err)
	}
	return n == 1, nil
}

// MarkSent moves a claimed message to the terminal 'sent' state, recording the
// provider and its message id and clearing the lock. sent_at and finished_at
// are both set to now.
func (r *MessageRepository) MarkSent(ctx context.Context, id, providerName, providerMessageID string, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE messages
		    SET status='sent', provider=?, provider_message_id=?, sent_at=?, finished_at=?,
		        locked_at=NULL, locked_by=NULL, updated_at=?
		  WHERE id=?`,
		providerName, providerMessageID, now, now, now, id)
	if err != nil {
		return fmt.Errorf("marking sent: %w", err)
	}
	return nil
}

// MarkRetry returns a message to 'queued' after a retryable failure, scheduling
// the next attempt and clearing the lock so the sweeper (and the armed timer)
// can re-dispatch it.
func (r *MessageRepository) MarkRetry(ctx context.Context, id, providerName, lastErr string, nextAttemptAt, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE messages
		    SET status='queued', provider=?, last_error=?, next_attempt_at=?,
		        locked_at=NULL, locked_by=NULL, updated_at=?
		  WHERE id=?`,
		providerName, lastErr, nextAttemptAt, now, id)
	if err != nil {
		return fmt.Errorf("marking retry: %w", err)
	}
	return nil
}

// MarkFailed moves a message to the terminal 'failed' state (permanent error or
// attempts exhausted), recording the last error and clearing the lock.
func (r *MessageRepository) MarkFailed(ctx context.Context, id, providerName, lastErr string, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE messages
		    SET status='failed', provider=?, last_error=?, finished_at=?,
		        locked_at=NULL, locked_by=NULL, updated_at=?
		  WHERE id=?`,
		providerName, lastErr, now, now, id)
	if err != nil {
		return fmt.Errorf("marking failed: %w", err)
	}
	return nil
}

// UpdateRecipientsStatus transitions every recipient of a message currently in
// fromStatus to toStatus (e.g. pending→sent after the provider accepts). It
// returns the number of recipients updated. Suppressed recipients (a different
// status) are left untouched.
func (r *MessageRepository) UpdateRecipientsStatus(ctx context.Context, messageID, fromStatus, toStatus string) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE message_recipients SET status=? WHERE message_id=? AND status=?`,
		toStatus, messageID, fromStatus)
	if err != nil {
		return 0, fmt.Errorf("updating recipient statuses: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("recipient update rows affected: %w", err)
	}
	return n, nil
}

// LoadAttachments returns a message's attachments (including content) ordered
// by id, for building the outbound MIME message.
func (r *MessageRepository) LoadAttachments(ctx context.Context, messageID string) ([]*entity.MessageAttachment, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, message_id, filename, content_type, size_bytes, content, purged_at
		   FROM message_attachments WHERE message_id = ? ORDER BY id`, messageID)
	if err != nil {
		return nil, fmt.Errorf("querying attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*entity.MessageAttachment
	for rows.Next() {
		var att entity.MessageAttachment
		if err := rows.Scan(&att.ID, &att.MessageID, &att.Filename, &att.ContentType,
			&att.SizeBytes, &att.Content, &att.PurgedAt); err != nil {
			return nil, fmt.Errorf("scanning attachment: %w", err)
		}
		out = append(out, &att)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating attachments: %w", err)
	}
	return out, nil
}

// FindDueQueued returns up to limit ids of queued, due messages (oldest first),
// skipping any id in excludeIDs (rows already in flight in this process). The
// sweeper uses it to drain the backlog into the worker pool.
func (r *MessageRepository) FindDueQueued(ctx context.Context, now time.Time, limit int, excludeIDs []string) ([]string, error) {
	query := `SELECT id FROM messages
	           WHERE status='queued' AND (next_attempt_at IS NULL OR next_attempt_at <= ?)`
	args := []any{now}
	if len(excludeIDs) > 0 {
		query += ` AND id NOT IN (` + placeholders(len(excludeIDs)) + `)`
		for _, id := range excludeIDs {
			args = append(args, id)
		}
	}
	query += ` ORDER BY created_at LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("finding due queued: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning queued id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating queued ids: %w", err)
	}
	return ids, nil
}

// UnstickStaleSending returns to 'queued' any message stuck in 'sending' whose
// lock predates olderThan (a crashed worker never released it). It returns the
// number of rows recovered.
func (r *MessageRepository) UnstickStaleSending(ctx context.Context, olderThan, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE messages
		    SET status='queued', locked_at=NULL, locked_by=NULL, updated_at=?
		  WHERE status='sending' AND locked_at < ?`,
		now, olderThan)
	if err != nil {
		return 0, fmt.Errorf("unsticking stale sending: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("unstick rows affected: %w", err)
	}
	return n, nil
}

// PurgeAttachmentsBatch nulls the content (setting purged_at) of up to limit
// attachments belonging to messages that reached a terminal status before
// cutoff. It returns the number of attachments purged; the caller loops until
// it returns 0. Attachments already purged (content NULL) are skipped.
func (r *MessageRepository) PurgeAttachmentsBatch(ctx context.Context, cutoff time.Time, limit int, now time.Time) (int64, error) {
	// Multi-table UPDATE cannot carry a LIMIT in MySQL, so select the batch of
	// ids first, then update exactly those.
	rows, err := r.db.QueryContext(ctx,
		`SELECT a.id FROM message_attachments a JOIN messages m ON m.id = a.message_id
		  WHERE a.content IS NOT NULL AND m.finished_at IS NOT NULL AND m.finished_at < ?
		  LIMIT ?`, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("selecting attachments to purge: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scanning attachment id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("iterating attachment ids: %w", err)
	}
	_ = rows.Close()
	if len(ids) == 0 {
		return 0, nil
	}

	args := []any{now}
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE message_attachments SET content=NULL, purged_at=? WHERE id IN (`+placeholders(len(ids))+`)`,
		args...)
	if err != nil {
		return 0, fmt.Errorf("purging attachments: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("purge rows affected: %w", err)
	}
	return n, nil
}

// DeleteHistoryBatch deletes up to limit messages created before cutoff, along
// with their events, recipients and attachments, in one transaction. It returns
// the number of messages deleted; the caller loops until it returns 0.
func (r *MessageRepository) DeleteHistoryBatch(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning history delete tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM messages WHERE created_at < ? LIMIT ?`, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("selecting messages to delete: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scanning message id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("iterating message ids: %w", err)
	}
	_ = rows.Close()
	if len(ids) == 0 {
		return 0, nil
	}

	in := placeholders(len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	for _, table := range []string{"message_events", "message_recipients", "message_attachments"} {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE message_id IN (`+in+`)`, args...); err != nil {
			return 0, fmt.Errorf("deleting from %s: %w", table, err)
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id IN (`+in+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("deleting messages: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete rows affected: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing history delete: %w", err)
	}
	return n, nil
}

// placeholders returns "?, ?, …" with n parameters for an IN clause.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
