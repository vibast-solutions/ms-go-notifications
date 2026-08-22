package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
)

// EmailService implements the consumer accept path (POST /v1/emails) and
// status read-back (GET /v1/emails/{id}).
type EmailService struct {
	messages     MessageRepository
	suppressions SuppressionRepository
	dispatcher   Dispatcher
	limits       dtov1.Limits

	now func() time.Time
}

// NewEmailService wires the email service with its repositories, the dispatch
// hand-off and the config-derived validation limits.
func NewEmailService(messages MessageRepository, suppressions SuppressionRepository,
	dispatcher Dispatcher, limits dtov1.Limits) *EmailService {
	return &EmailService{
		messages:     messages,
		suppressions: suppressions,
		dispatcher:   dispatcher,
		limits:       limits,
		now:          time.Now,
	}
}

// Accept validates and stores a send request, then hands the id to the
// dispatcher. It returns the stored message and whether it was newly created
// (false on an idempotent replay). Errors are the domain errors mapped to HTTP
// by the controller: *ValidationError (400), ErrDomainNotAllowed (403),
// ErrPayloadTooLarge (413).
func (s *EmailService) Accept(ctx context.Context, apiKey *entity.APIKey, req *dtov1.SendEmailRequest) (*entity.Message, bool, error) {
	if fields := req.Validate(s.limits); fields != nil {
		return nil, false, NewValidationError(fields)
	}
	if req.DecodedPayloadBytes() > s.limits.MaxPayloadBytes {
		return nil, false, ErrPayloadTooLarge
	}
	if !keyAllowsDomain(apiKey, domainOf(req.From.Email)) {
		return nil, false, ErrDomainNotAllowed
	}

	// Idempotency: a hit short-circuits to the existing message (200, no
	// dispatch). The payload is not compared (Business Logic §3).
	if req.IdempotencyKey != "" {
		existing, err := s.messages.FindByIdempotency(ctx, apiKey.ID, req.IdempotencyKey)
		switch {
		case err == nil:
			return existing, false, nil
		case !errors.Is(err, sql.ErrNoRows):
			return nil, false, fmt.Errorf("idempotency lookup: %w", err)
		}
	}

	suppressed, err := s.loadSuppressed(ctx, req)
	if err != nil {
		return nil, false, err
	}

	now := s.now().UTC().Truncate(time.Millisecond)
	msg, recipients, attachments, events := buildMessage(apiKey, req, suppressed, now)

	created, err := s.persist(ctx, msg, recipients, attachments, events)
	if err != nil {
		return nil, false, err
	}
	if !created {
		// Lost the idempotency race — return the row the winner stored.
		existing, ferr := s.messages.FindByIdempotency(ctx, apiKey.ID, req.IdempotencyKey)
		if ferr != nil {
			return nil, false, fmt.Errorf("re-reading after idempotency race: %w", ferr)
		}
		return existing, false, nil
	}

	if msg.Status == entity.MessageStatusQueued {
		s.dispatcher.Enqueue(msg.ID)
	}
	return msg, true, nil
}

// persist writes the message and children in one transaction. It returns
// (false, nil) when the write lost the (api_key_id, idempotency_key) race so
// the caller can re-read the winning row.
func (s *EmailService) persist(ctx context.Context, msg *entity.Message,
	recipients []*entity.MessageRecipient, attachments []*entity.MessageAttachment,
	events []*entity.MessageEvent) (bool, error) {
	tx, err := s.messages.BeginTx(ctx)
	if err != nil {
		return false, fmt.Errorf("beginning tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.messages.CreateWithChildren(ctx, tx, msg, recipients, attachments, events); err != nil {
		if errors.Is(err, repository.ErrDuplicateKey) {
			return false, nil
		}
		return false, fmt.Errorf("creating message: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing tx: %w", err)
	}
	return true, nil
}

// loadSuppressed returns the set of suppressed recipient addresses for req.
func (s *EmailService) loadSuppressed(ctx context.Context, req *dtov1.SendEmailRequest) (map[string]bool, error) {
	emails := allRecipientEmails(req)
	rows, err := s.suppressions.FindByEmails(ctx, emails)
	if err != nil {
		return nil, fmt.Errorf("loading suppressions: %w", err)
	}
	set := make(map[string]bool, len(rows))
	for _, r := range rows {
		set[strings.ToLower(r.Email)] = true
	}
	return set, nil
}

// GetForAPIKey returns a message (with recipients and events) owned by
// apiKeyID. Returns ErrNotFound when the id is unknown or belongs to another
// key, so the caller cannot probe other keys' messages.
func (s *EmailService) GetForAPIKey(ctx context.Context, id, apiKeyID string) (*entity.Message, []*entity.MessageRecipient, []*entity.MessageEvent, error) {
	msg, err := s.messages.FindByIDForAPIKey(ctx, id, apiKeyID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil, ErrNotFound
		}
		return nil, nil, nil, fmt.Errorf("loading message: %w", err)
	}
	recipients, err := s.messages.LoadRecipients(ctx, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading recipients: %w", err)
	}
	events, err := s.messages.LoadEvents(ctx, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading events: %w", err)
	}
	return msg, recipients, events, nil
}

// buildMessage assembles the message, recipient, attachment and event rows from
// a validated request. Recipients present in suppressed are stored with status
// suppressed and excluded from sending; when every recipient is suppressed the
// message itself is stored suppressed (terminal, never dispatched).
func buildMessage(apiKey *entity.APIKey, req *dtov1.SendEmailRequest, suppressed map[string]bool, now time.Time) (
	*entity.Message, []*entity.MessageRecipient, []*entity.MessageAttachment, []*entity.MessageEvent) {

	id := entity.NewID()

	recipients := make([]*entity.MessageRecipient, 0)
	sendable := 0
	appendRecipients := func(list []dtov1.Address, typ string) {
		for _, a := range list {
			status := entity.RecipientStatusPending
			if suppressed[a.Email] {
				status = entity.RecipientStatusSuppressed
			} else {
				sendable++
			}
			recipients = append(recipients, &entity.MessageRecipient{
				ID:        entity.NewID(),
				MessageID: id,
				Type:      typ,
				Email:     a.Email,
				Name:      optionalString(a.Name),
				Status:    status,
			})
		}
	}
	appendRecipients(req.To, entity.RecipientTypeTo)
	appendRecipients(req.Cc, entity.RecipientTypeCc)
	appendRecipients(req.Bcc, entity.RecipientTypeBcc)

	status := entity.MessageStatusQueued
	eventType := entity.EventTypeQueued
	var finishedAt *time.Time
	if sendable == 0 {
		status = entity.MessageStatusSuppressed
		eventType = entity.EventTypeSuppressed
		finishedAt = &now
	}

	msg := &entity.Message{
		ID:             id,
		APIKeyID:       apiKey.ID,
		Channel:        entity.ChannelEmail,
		Status:         status,
		FromEmail:      req.From.Email,
		FromName:       optionalString(req.From.Name),
		FromDomain:     domainOf(req.From.Email),
		ReplyTo:        toEmailAddresses(req.ReplyTo),
		Subject:        req.Subject,
		TextBody:       optionalString(req.Text),
		HTMLBody:       optionalString(req.HTML),
		Headers:        req.Headers,
		Tags:           req.Tags,
		Metadata:       req.Metadata,
		IdempotencyKey: optionalString(req.IdempotencyKey),
		RecipientCount: len(recipients),
		CreatedAt:      now,
		UpdatedAt:      now,
		FinishedAt:     finishedAt,
	}

	attachments := make([]*entity.MessageAttachment, 0, len(req.Attachments))
	for i := range req.Attachments {
		a := &req.Attachments[i]
		attachments = append(attachments, &entity.MessageAttachment{
			ID:          entity.NewID(),
			MessageID:   id,
			Filename:    a.Filename,
			ContentType: a.ContentType,
			SizeBytes:   len(a.Decoded()),
			Content:     a.Decoded(),
		})
	}

	events := []*entity.MessageEvent{{
		ID:         entity.NewID(),
		MessageID:  id,
		Type:       eventType,
		OccurredAt: now,
		CreatedAt:  now,
	}}

	return msg, recipients, attachments, events
}

// allRecipientEmails returns every recipient address across to/cc/bcc.
func allRecipientEmails(req *dtov1.SendEmailRequest) []string {
	out := make([]string, 0, len(req.To)+len(req.Cc)+len(req.Bcc))
	for _, list := range [][]dtov1.Address{req.To, req.Cc, req.Bcc} {
		for _, a := range list {
			out = append(out, a.Email)
		}
	}
	return out
}

// keyAllowsDomain reports whether domain is one of the key's sender domains.
func keyAllowsDomain(key *entity.APIKey, domain string) bool {
	if domain == "" {
		return false
	}
	for _, d := range key.Domains {
		if d == domain {
			return true
		}
	}
	return false
}

// domainOf returns the lower-cased domain part of an already-normalized email.
func domainOf(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 {
		return email[i+1:]
	}
	return ""
}

// toEmailAddresses converts request addresses to the entity shape used in the
// reply_to JSON column.
func toEmailAddresses(addrs []dtov1.Address) []entity.EmailAddress {
	if len(addrs) == 0 {
		return nil
	}
	out := make([]entity.EmailAddress, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, entity.EmailAddress{Email: a.Email, Name: a.Name})
	}
	return out
}

// optionalString returns a pointer to s, or nil when s is empty, for NULLable
// columns.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
