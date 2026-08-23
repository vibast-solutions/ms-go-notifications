package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/pkg/provider/ses"
)

// EventService applies SES provider events (delivered via SNS) to a message:
// it records each event on the timeline, updates per-recipient delivery status,
// grows the suppression list on permanent bounces and complaints, and records
// rejection reasons — all idempotently, so a redelivered SNS message is a
// no-op. It implements Business Logic §7.
type EventService struct {
	messages     EventMessageRepository
	events       EventStore
	suppressions SuppressionWriter
	log          log.FieldLogger

	now func() time.Time
}

// NewEventService wires the event service with its repositories and logger.
func NewEventService(messages EventMessageRepository, events EventStore,
	suppressions SuppressionWriter, logger log.FieldLogger) *EventService {
	if logger == nil {
		logger = log.StandardLogger()
	}
	return &EventService{
		messages:     messages,
		events:       events,
		suppressions: suppressions,
		log:          logger,
		now:          time.Now,
	}
}

// HandleSESEvent processes one SES event that arrived under the given SNS
// MessageId (used as the per-recipient dedupe key). An event whose message id
// does not resolve to a stored message is logged at WARN and dropped (no orphan
// table). Errors returned are infrastructure failures; the caller answers 5xx so
// SNS retries. Everything the rules define as terminal (unknown id, unknown
// event type) returns nil so the caller answers 200.
func (s *EventService) HandleSESEvent(ctx context.Context, snsMessageID string, ev *ses.Event) error {
	msg, err := s.messages.FindByProviderMessageID(ctx, ev.MessageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.log.WithFields(log.Fields{
				"provider_message_id": ev.MessageID,
				"sns_message_id":      snsMessageID,
				"event_type":          ev.Type,
			}).Warn("ses event for unknown provider message id; dropping")
			return nil
		}
		return fmt.Errorf("finding message by provider id: %w", err)
	}

	now := s.now().UTC().Truncate(time.Millisecond)
	occurred := ev.Timestamp
	if occurred.IsZero() {
		occurred = now
	}

	// Reject is message-level: record the reason, no per-recipient status change.
	if ev.Type == ses.EventReject && ev.Reject != nil {
		if err := s.messages.SetLastError(ctx, msg.ID, ev.Reject.Reason, now); err != nil {
			return err
		}
	}

	eventType := timelineEventType(ev.Type)
	status := recipientStatusFor(ev.Type)
	details := indexDetails(ev.StatusRecipients())

	for _, raw := range ev.TimelineRecipients() {
		addr := normalizeAddr(raw)

		inserted, err := s.insertEvent(ctx, msg.ID, addr, eventType, snsMessageID, occurred, now, ev.Raw)
		if err != nil {
			return err
		}
		// A duplicate (provider_event_id, recipient_email) means this SNS
		// message was already applied for this recipient — skip its effects.
		if !inserted {
			continue
		}
		if addr == "" || status == "" {
			continue
		}
		if err := s.applyRecipient(ctx, msg.ID, addr, status, ev.Type, details[addr], occurred); err != nil {
			return err
		}
	}
	return nil
}

// insertEvent appends one timeline event and reports whether it was new (false
// on a dedupe hit). recipient_email is stored as NULL when addr is empty.
func (s *EventService) insertEvent(ctx context.Context, messageID, addr, eventType, snsMessageID string,
	occurred, created time.Time, payload []byte) (bool, error) {
	ev := &entity.MessageEvent{
		ID:              entity.NewID(),
		MessageID:       messageID,
		Type:            eventType,
		ProviderEventID: &snsMessageID,
		Payload:         payload,
		OccurredAt:      occurred,
		CreatedAt:       created,
	}
	if addr != "" {
		ev.RecipientEmail = &addr
	}
	err := s.events.Insert(ctx, ev)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, repository.ErrDuplicateKey):
		return false, nil
	default:
		return false, fmt.Errorf("recording event: %w", err)
	}
}

// applyRecipient updates one recipient's status and, for permanent bounces and
// complaints, adds it to the suppression list.
func (s *EventService) applyRecipient(ctx context.Context, messageID, addr, status, evType string,
	d ses.RecipientDetail, at time.Time) error {
	bounceType := optional(d.BounceType)
	bounceSubtype := optional(d.BounceSubtype)
	diagnostic := optional(d.Diagnostic)

	if err := s.messages.SetRecipientStatus(ctx, messageID, addr, status,
		bounceType, bounceSubtype, diagnostic, at); err != nil {
		return err
	}

	reason := ""
	switch {
	case evType == ses.EventBounce && strings.EqualFold(d.BounceType, ses.BounceTypePermanent):
		reason = entity.SuppressionReasonHardBounce
	case evType == ses.EventComplaint:
		reason = entity.SuppressionReasonComplaint
	}
	if reason == "" {
		return nil
	}

	src := messageID
	if err := s.suppressions.Upsert(ctx, &entity.Suppression{
		Email:           addr,
		Reason:          reason,
		SourceMessageID: &src,
		CreatedAt:       at,
	}); err != nil {
		return err
	}
	return nil
}

// recipientStatusFor maps an SES event type to the recipient status it sets, or
// "" when the event changes no recipient status (Send, Reject, Open, Click,
// unknown types).
func recipientStatusFor(evType string) string {
	switch evType {
	case ses.EventDelivery:
		return entity.RecipientStatusDelivered
	case ses.EventBounce:
		return entity.RecipientStatusBounced
	case ses.EventComplaint:
		return entity.RecipientStatusComplained
	case ses.EventDeliveryDelay:
		return entity.RecipientStatusDelayed
	default:
		return ""
	}
}

// timelineEventType maps an SES event type to the message_events.type stored on
// the timeline. Unrecognised types are recorded as "other".
func timelineEventType(evType string) string {
	switch evType {
	case ses.EventDelivery:
		return entity.EventTypeDelivery
	case ses.EventBounce:
		return entity.EventTypeBounce
	case ses.EventComplaint:
		return entity.EventTypeComplaint
	case ses.EventDeliveryDelay:
		return entity.EventTypeDeliveryDelay
	case ses.EventReject:
		return entity.EventTypeReject
	case ses.EventSend:
		return entity.EventTypeSend
	case ses.EventOpen:
		return entity.EventTypeOpen
	case ses.EventClick:
		return entity.EventTypeClick
	default:
		return entity.EventTypeOther
	}
}

// indexDetails keys per-recipient details by normalized address for lookup
// while iterating the timeline recipients.
func indexDetails(details []ses.RecipientDetail) map[string]ses.RecipientDetail {
	out := make(map[string]ses.RecipientDetail, len(details))
	for _, d := range details {
		out[normalizeAddr(d.Email)] = d
	}
	return out
}

// normalizeAddr lower-cases and trims an address so provider-reported
// recipients match the lower-cased addresses stored at accept time.
func normalizeAddr(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// optional returns a pointer to s, or nil when empty, for NULLable columns.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
