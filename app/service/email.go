package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/lock"
	"github.com/vibast-solutions/ms-go-notifications/app/preparer"
	"github.com/vibast-solutions/ms-go-notifications/app/provider"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
)

type EmailService struct {
	preparer preparer.EmailPreparer
	provider provider.EmailProvider
	history  *repository.EmailHistoryRepository
	locker   lock.Locker
}

// NewEmailService builds the email service with dependencies.
func NewEmailService(preparer preparer.EmailPreparer, provider provider.EmailProvider, history *repository.EmailHistoryRepository, locker lock.Locker) *EmailService {
	return &EmailService{preparer: preparer, provider: provider, history: history, locker: locker}
}

// CreateRequest records an email send request in history.
func (s *EmailService) CreateRequest(ctx context.Context, requestID string, recipient string, subject string, content string) error {
	if err := s.history.Create(ctx, requestID, recipient, subject, content, entity.EmailStatusNew); err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return ErrDuplicateRequestID
		}
		return err
	}
	return nil
}

// DeleteRequest removes a history entry by request ID.
func (s *EmailService) DeleteRequest(ctx context.Context, requestID string) error {
	return s.history.DeleteByRequestID(ctx, requestID)
}

// SendRaw prepares, sends, and updates history for a raw email request.
func (s *EmailService) SendRaw(ctx context.Context, recipient string, subject string, content string) error {
	requestID, ok := RequestIDFromContext(ctx)
	if !ok || requestID == "" {
		return fmt.Errorf("request_id is required in context")
	}
	if recipient == "" {
		return fmt.Errorf("recipient is required")
	}
	if subject == "" {
		return fmt.Errorf("subject is required")
	}
	if content == "" {
		return fmt.Errorf("content is required")
	}

	lockKey := fmt.Sprintf("notifications:email:%s", requestID)
	if err := s.locker.Acquire(ctx, lockKey, 2*time.Minute); err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer func() {
		_ = s.locker.Release(context.Background(), lockKey)
	}()

	if err := s.history.UpdateStatus(ctx, requestID, entity.EmailStatusProcessing); err != nil {
		return fmt.Errorf("update status to processing: %w", err)
	}

	raw, err := s.preparer.Prepare(ctx, recipient, subject, content)
	if err != nil {
		if updateErr := s.history.UpdateStatus(ctx, requestID, entity.EmailStatusTemporaryFailure); updateErr != nil {
			return fmt.Errorf("prepare email content: %v; update status: %w", err, updateErr)
		}
		return fmt.Errorf("prepare email content: %w", err)
	}

	if err := s.history.UpdateContent(ctx, requestID, string(raw)); err != nil {
		if updateErr := s.history.UpdateStatus(ctx, requestID, entity.EmailStatusTemporaryFailure); updateErr != nil {
			return fmt.Errorf("update email history content: %v; update status: %w", err, updateErr)
		}
		return fmt.Errorf("update email history content: %w", err)
	}

	if err := s.provider.SendRaw(ctx, recipient, raw); err != nil {
		if updateErr := s.history.UpdateStatus(ctx, requestID, entity.EmailStatusPermanentFailure); updateErr != nil {
			return fmt.Errorf("send failed: %v; update status: %w", err, updateErr)
		}
		return err
	}

	if err := s.history.UpdateStatus(ctx, requestID, entity.EmailStatusSuccess); err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}
