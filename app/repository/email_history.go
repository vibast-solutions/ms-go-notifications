package repository

import (
	"context"
	"database/sql"
)

type EmailHistoryRepository struct {
	db *sql.DB
}

// NewEmailHistoryRepository constructs a repository backed by MySQL.
func NewEmailHistoryRepository(db *sql.DB) *EmailHistoryRepository {
	return &EmailHistoryRepository{db: db}
}

// Create inserts a new email history record.
func (r *EmailHistoryRepository) Create(ctx context.Context, requestID string, recipient string, subject string, content string, status int16) error {
	const query = `
		INSERT INTO email_history (request_id, recipient, subject, content, status, retries)
		VALUES (?, ?, ?, ?, ?, 0)
	`
	_, err := r.db.ExecContext(ctx, query, requestID, recipient, subject, content, status)
	return err
}

// DeleteByRequestID removes a history record by request ID.
func (r *EmailHistoryRepository) DeleteByRequestID(ctx context.Context, requestID string) error {
	const query = `
		DELETE FROM email_history
		WHERE request_id = ?
	`
	_, err := r.db.ExecContext(ctx, query, requestID)
	return err
}

// UpdateStatus updates the status for a request ID.
func (r *EmailHistoryRepository) UpdateStatus(ctx context.Context, requestID string, status int16) error {
	const query = `
		UPDATE email_history
		SET status = ?
		WHERE request_id = ?
	`
	_, err := r.db.ExecContext(ctx, query, status, requestID)
	return err
}

// UpdateContent updates the stored raw content for a request ID.
func (r *EmailHistoryRepository) UpdateContent(ctx context.Context, requestID string, content string) error {
	const query = `
		UPDATE email_history
		SET content = ?
		WHERE request_id = ?
	`
	_, err := r.db.ExecContext(ctx, query, content, requestID)
	return err
}
