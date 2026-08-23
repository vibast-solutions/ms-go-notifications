package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"time"
	"unicode/utf8"

	logrus "github.com/sirupsen/logrus"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/pkg/mime"
	"github.com/vibast-solutions/ms-go-notifications/pkg/provider"
)

// maxBackoff caps the exponential retry delay (Business Logic §6).
const maxBackoff = 15 * time.Minute

// lastErrorMaxLen bounds what is stored in messages.last_error / event payloads.
const lastErrorMaxLen = 1000

// ProcessorConfig configures retry behaviour.
type ProcessorConfig struct {
	MaxAttempts int           // SEND_MAX_ATTEMPTS
	BackoffBase time.Duration // SEND_BACKOFF_BASE
}

// Processor runs one message through the send pipeline: claim → load → build
// MIME → send → record the outcome (sent / retry / failed). The claim is the
// lock, so the same id handled by two instances results in exactly one send.
type Processor struct {
	store    MessageStore
	events   EventInserter
	provider provider.EmailProvider
	enqueuer Enqueuer
	cfg      ProcessorConfig
	log      logrus.FieldLogger

	lockedBy  string
	clock     func() time.Time
	jitter    func(time.Duration) time.Duration
	afterFunc func(time.Duration, func()) *time.Timer
}

// NewProcessor wires a Processor. locked_by is set to hostname:pid so a stuck
// row can be traced to the instance that held it.
func NewProcessor(store MessageStore, events EventInserter, prov provider.EmailProvider,
	enqueuer Enqueuer, cfg ProcessorConfig, logger logrus.FieldLogger) *Processor {
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = 5 * time.Second
	}
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	host, _ := os.Hostname()
	return &Processor{
		store:     store,
		events:    events,
		provider:  prov,
		enqueuer:  enqueuer,
		cfg:       cfg,
		log:       logger,
		lockedBy:  fmt.Sprintf("%s:%d", host, os.Getpid()),
		clock:     time.Now,
		jitter:    defaultJitter,
		afterFunc: time.AfterFunc,
	}
}

// Process handles a single message id. It is safe to call for an id that is not
// ours or not yet due: the claim simply affects zero rows and Process returns
// without sending.
func (p *Processor) Process(ctx context.Context, id string) {
	now := p.now()

	claimed, err := p.store.Claim(ctx, id, p.lockedBy, now)
	if err != nil {
		p.log.WithError(err).WithField("message_id", id).Error("claiming message")
		return
	}
	if !claimed {
		return // another worker owns it, or it is no longer due
	}

	msg, err := p.store.FindByID(ctx, id)
	if err != nil {
		p.log.WithError(err).WithField("message_id", id).Error("loading claimed message")
		return
	}
	log := p.log.WithFields(logrus.Fields{
		"message_id": msg.ID,
		"attempt":    msg.Attempts,
		"provider":   p.provider.Name(),
	})

	recipients, err := p.store.LoadRecipients(ctx, id)
	if err != nil {
		log.WithError(err).Error("loading recipients")
		return
	}
	attachments, err := p.store.LoadAttachments(ctx, id)
	if err != nil {
		log.WithError(err).Error("loading attachments")
		return
	}

	out, err := buildOutbound(msg, recipients, attachments, now)
	if err != nil {
		// The message we assembled is malformed — retrying will not help.
		log.WithError(err).Error("building outbound message")
		p.fail(ctx, msg, "building message: "+err.Error(), now, log)
		return
	}

	pmid, sendErr := p.provider.Send(ctx, out)
	if sendErr == nil {
		p.succeed(ctx, msg, pmid, now, log)
		return
	}

	if p.retryable(sendErr) && msg.Attempts < p.cfg.MaxAttempts {
		p.retry(ctx, msg, sendErr, now, log)
		return
	}
	p.fail(ctx, msg, sendErr.Error(), now, log)
}

// retryable reports whether err should be retried. Unknown errors (anything not
// classified as a *provider.ProviderError) default to retryable per §6.
func (p *Processor) retryable(err error) bool {
	var pe *provider.ProviderError
	if errors.As(err, &pe) {
		return pe.Retryable
	}
	return true
}

func (p *Processor) succeed(ctx context.Context, msg *entity.Message, pmid string, now time.Time, log logrus.FieldLogger) {
	if err := p.store.MarkSent(ctx, msg.ID, p.provider.Name(), pmid, now); err != nil {
		// The row stays 'sending'; the sweeper's stale-lock recovery will
		// re-queue it. Do not record success we could not persist.
		log.WithError(err).Error("marking sent")
		return
	}
	if _, err := p.store.UpdateRecipientsStatus(ctx, msg.ID, entity.RecipientStatusPending, entity.RecipientStatusSent); err != nil {
		log.WithError(err).Warn("updating recipients to sent")
	}
	p.insertEvent(ctx, msg.ID, entity.EventTypeAttempt, attemptPayload{
		Outcome:           "sent",
		Provider:          p.provider.Name(),
		ProviderMessageID: pmid,
	}, now)
	log.WithField("provider_message_id", pmid).Info("email sent")
}

func (p *Processor) retry(ctx context.Context, msg *entity.Message, sendErr error, now time.Time, log logrus.FieldLogger) {
	backoff := p.backoff(msg.Attempts)
	next := now.Add(backoff)
	errText := truncate(sendErr.Error(), lastErrorMaxLen)

	if err := p.store.MarkRetry(ctx, msg.ID, p.provider.Name(), errText, next, now); err != nil {
		log.WithError(err).Error("marking retry")
		return
	}
	p.insertEvent(ctx, msg.ID, entity.EventTypeAttempt, attemptPayload{
		Outcome:  "retry",
		Provider: p.provider.Name(),
		Error:    errText,
	}, now)
	log.WithError(sendErr).WithFields(logrus.Fields{
		"backoff":         backoff.String(),
		"next_attempt_at": next,
	}).Warn("send failed, scheduled retry")

	// Fast path: re-dispatch when the backoff elapses. The sweeper is the
	// safety net if this process dies before the timer fires.
	id := msg.ID
	p.afterFunc(backoff, func() { p.enqueuer.Enqueue(id) })
}

func (p *Processor) fail(ctx context.Context, msg *entity.Message, reason string, now time.Time, log logrus.FieldLogger) {
	errText := truncate(reason, lastErrorMaxLen)
	if err := p.store.MarkFailed(ctx, msg.ID, p.provider.Name(), errText, now); err != nil {
		log.WithError(err).Error("marking failed")
		return
	}
	p.insertEvent(ctx, msg.ID, entity.EventTypeAttempt, attemptPayload{
		Outcome:  "failed",
		Provider: p.provider.Name(),
		Error:    errText,
	}, now)
	p.insertEvent(ctx, msg.ID, entity.EventTypeFailed, attemptPayload{
		Provider: p.provider.Name(),
		Error:    errText,
	}, now)
	log.WithField("last_error", errText).Error("send failed permanently")
}

// backoff computes min(base × 2^(attempts-1), 15m) with ±20% jitter.
func (p *Processor) backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := time.Duration(float64(p.cfg.BackoffBase) * math.Pow(2, float64(attempts-1)))
	if d <= 0 || d > maxBackoff {
		d = maxBackoff
	}
	return p.jitter(d)
}

// insertEvent appends a message event, logging (but not failing the send on) a
// write error — the audit trail is best-effort relative to the message state.
func (p *Processor) insertEvent(ctx context.Context, messageID, eventType string, payload attemptPayload, now time.Time) {
	raw, err := json.Marshal(payload)
	if err != nil {
		p.log.WithError(err).WithField("message_id", messageID).Warn("marshaling event payload")
		return
	}
	ev := &entity.MessageEvent{
		ID:         entity.NewID(),
		MessageID:  messageID,
		Type:       eventType,
		Payload:    raw,
		OccurredAt: now,
		CreatedAt:  now,
	}
	if err := p.events.Insert(ctx, ev); err != nil {
		p.log.WithError(err).WithFields(logrus.Fields{
			"message_id": messageID,
			"event_type": eventType,
		}).Warn("inserting message event")
	}
}

func (p *Processor) now() time.Time {
	return p.clock().UTC().Truncate(time.Millisecond)
}

// attemptPayload is the JSON stored in an attempt/failed event's payload.
type attemptPayload struct {
	Outcome           string `json:"outcome,omitempty"`
	Provider          string `json:"provider,omitempty"`
	ProviderMessageID string `json:"providerMessageId,omitempty"`
	Error             string `json:"error,omitempty"`
}

// defaultJitter perturbs d by ±20%.
func defaultJitter(d time.Duration) time.Duration {
	factor := 0.8 + rand.Float64()*0.4 //nolint:gosec // jitter, not security-sensitive
	return time.Duration(float64(d) * factor)
}

// truncate shortens s to at most n bytes (on a rune boundary) for storage.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	trimmed := s[:n]
	for len(trimmed) > 0 && !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}

// buildOutbound assembles the provider payload from a message and its
// recipients/attachments. Suppressed recipients are excluded from both the MIME
// headers and the envelope; Bcc is carried only in the envelope.
func buildOutbound(msg *entity.Message, recipients []*entity.MessageRecipient,
	attachments []*entity.MessageAttachment, now time.Time) (*provider.OutboundEmail, error) {

	var (
		mimeTo, mimeCc []mime.Address
		envTo, envCc   []string
		envBcc         []string
	)
	for _, r := range recipients {
		if r.Status == entity.RecipientStatusSuppressed {
			continue
		}
		switch r.Type {
		case entity.RecipientTypeTo:
			mimeTo = append(mimeTo, mime.Address{Name: deref(r.Name), Email: r.Email})
			envTo = append(envTo, r.Email)
		case entity.RecipientTypeCc:
			mimeCc = append(mimeCc, mime.Address{Name: deref(r.Name), Email: r.Email})
			envCc = append(envCc, r.Email)
		case entity.RecipientTypeBcc:
			envBcc = append(envBcc, r.Email) // envelope only, never in headers
		}
	}

	replyTo := make([]mime.Address, 0, len(msg.ReplyTo))
	envReplyTo := make([]string, 0, len(msg.ReplyTo))
	for _, a := range msg.ReplyTo {
		replyTo = append(replyTo, mime.Address{Name: a.Name, Email: a.Email})
		envReplyTo = append(envReplyTo, a.Email)
	}

	mimeAtts := make([]mime.Attachment, 0, len(attachments))
	for _, a := range attachments {
		if a.Content == nil {
			continue // purged; nothing to attach
		}
		mimeAtts = append(mimeAtts, mime.Attachment{
			Filename:    a.Filename,
			ContentType: a.ContentType,
			Content:     a.Content,
		})
	}

	raw, err := mime.Build(&mime.Message{
		MessageID:   msg.ID,
		Date:        now,
		From:        mime.Address{Name: deref(msg.FromName), Email: msg.FromEmail},
		To:          mimeTo,
		Cc:          mimeCc,
		ReplyTo:     replyTo,
		Subject:     msg.Subject,
		Text:        deref(msg.TextBody),
		HTML:        deref(msg.HTMLBody),
		Headers:     msg.Headers,
		Attachments: mimeAtts,
	})
	if err != nil {
		return nil, err
	}

	return &provider.OutboundEmail{
		MessageID: msg.ID,
		From:      msg.FromEmail,
		To:        envTo,
		Cc:        envCc,
		Bcc:       envBcc,
		ReplyTo:   envReplyTo,
		Subject:   msg.Subject,
		Raw:       raw,
	}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
