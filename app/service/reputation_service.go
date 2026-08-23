package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// ReputationWindow is the trailing window over which bounce/complaint rates are
// computed (Business Logic §8).
const ReputationWindow = 24 * time.Hour

// Actors recorded in the sending_paused row's "by" field.
const (
	// ActorAuto marks a pause triggered by the circuit breaker itself.
	ActorAuto = "auto"
)

// ReputationConfig holds the circuit-breaker thresholds (Business Logic §8).
type ReputationConfig struct {
	BounceThreshold    float64 // REPUTATION_BOUNCE_THRESHOLD (fraction, e.g. 0.04)
	ComplaintThreshold float64 // REPUTATION_COMPLAINT_THRESHOLD (fraction, e.g. 0.0008)
	MinSample          int     // REPUTATION_MIN_SAMPLE — evaluate only above this many sent recipients
}

// SendingState is the current pause state of the service.
type SendingState struct {
	Paused bool
	Reason string
	Since  time.Time
	By     string
}

// sendingPausedValue is the JSON stored in settings.sending_paused.
type sendingPausedValue struct {
	Reason string    `json:"reason"`
	Since  time.Time `json:"since"`
	By     string    `json:"by"`
}

// ReputationService is the SES reputation circuit breaker. It computes trailing
// bounce/complaint rates, pauses sending automatically when a threshold is
// crossed, and exposes manual pause/resume for operators. Pausing only stops
// the workers and sweeper from claiming queued messages — the accept path keeps
// storing them as queued, so nothing is rejected while paused. Resume is always
// manual: the service never auto-resumes (Business Logic §8).
type ReputationService struct {
	settings SettingsStore
	stats    ReputationStore
	cfg      ReputationConfig
	log      log.FieldLogger

	now func() time.Time
}

// NewReputationService wires the circuit breaker with its stores, thresholds
// and logger.
func NewReputationService(settings SettingsStore, stats ReputationStore, cfg ReputationConfig, logger log.FieldLogger) *ReputationService {
	if logger == nil {
		logger = log.StandardLogger()
	}
	return &ReputationService{
		settings: settings,
		stats:    stats,
		cfg:      cfg,
		log:      logger,
		now:      time.Now,
	}
}

// ComputeRates returns the bounce and complaint rates over the trailing window,
// together with the raw counts they derive from. The rates are hard bounces and
// complaints divided by the number of non-suppressed recipients sent to in the
// window; both are 0 when nothing was sent.
func (s *ReputationService) ComputeRates(ctx context.Context, window time.Duration) (sentRecipients, hardBounces, complaints int, bounceRate, complaintRate float64, err error) {
	since := s.now().UTC().Add(-window)

	sentRecipients, err = s.stats.CountSentRecipientsSince(ctx, since)
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	hardBounces, complaints, err = s.stats.CountHardBouncesAndComplaintsSince(ctx, since)
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	if sentRecipients > 0 {
		bounceRate = float64(hardBounces) / float64(sentRecipients)
		complaintRate = float64(complaints) / float64(sentRecipients)
	}
	return sentRecipients, hardBounces, complaints, bounceRate, complaintRate, nil
}

// Evaluate runs one circuit-breaker check (the sweeper calls it every 60 s).
// When the sample is large enough and a rate is at or above its threshold, it
// pauses sending automatically — unless sending is already paused, in which case
// it leaves the existing pause (and its reason) untouched. It never resumes.
// Errors are logged, not returned: a failed evaluation must not disturb the
// sweep loop.
func (s *ReputationService) Evaluate(ctx context.Context) {
	sent, hardBounces, complaints, bounceRate, complaintRate, err := s.ComputeRates(ctx, ReputationWindow)
	if err != nil {
		s.log.WithError(err).Error("reputation: computing rates")
		return
	}
	if sent < s.cfg.MinSample {
		return // too little traffic to judge reputation
	}

	var reason string
	switch {
	case bounceRate >= s.cfg.BounceThreshold:
		reason = "auto: bounce " + pct(bounceRate) + " >= " + pct(s.cfg.BounceThreshold)
	case complaintRate >= s.cfg.ComplaintThreshold:
		reason = "auto: complaint " + pct(complaintRate) + " >= " + pct(s.cfg.ComplaintThreshold)
	default:
		return // within thresholds
	}

	state, err := s.State(ctx)
	if err != nil {
		s.log.WithError(err).Error("reputation: reading pause state")
		return
	}
	if state.Paused {
		return // already paused; keep the original reason/since/by
	}

	s.log.WithFields(log.Fields{
		"sent_recipients": sent,
		"hard_bounces":    hardBounces,
		"complaints":      complaints,
		"bounce_rate":     bounceRate,
		"complaint_rate":  complaintRate,
	}).Warn("reputation thresholds crossed; pausing sending")

	if err := s.Pause(ctx, reason, ActorAuto); err != nil {
		s.log.WithError(err).Error("reputation: pausing sending")
	}
}

// Pause records the sending_paused setting with the given reason and actor,
// stopping the workers and sweeper from claiming new messages. It is idempotent:
// pausing while already paused overwrites the reason/since/by (an operator
// re-pause), which is why the automatic path guards against it in Evaluate.
func (s *ReputationService) Pause(ctx context.Context, reason, by string) error {
	now := s.now().UTC().Truncate(time.Millisecond)
	value, err := json.Marshal(sendingPausedValue{Reason: reason, Since: now, By: by})
	if err != nil {
		return fmt.Errorf("encoding pause state: %w", err)
	}
	if err := s.settings.Upsert(ctx, entity.SettingSendingPaused, value, now); err != nil {
		return err
	}
	s.log.WithFields(log.Fields{"reason": reason, "by": by}).Warn("sending paused")
	return nil
}

// Resume clears the sending_paused setting, letting the sweeper re-dispatch the
// queued backlog. Resuming when not paused is a no-op. Resume is only ever
// triggered by an operator (Business Logic §8: never auto-resume).
func (s *ReputationService) Resume(ctx context.Context, by string) error {
	if err := s.settings.Delete(ctx, entity.SettingSendingPaused); err != nil {
		return err
	}
	s.log.WithField("by", by).Info("sending resumed")
	return nil
}

// State reports the current pause state, reading the sending_paused row. A
// missing row means sending is running. A present-but-unparseable value is
// treated as paused (fail safe) with empty details.
func (s *ReputationService) State(ctx context.Context) (SendingState, error) {
	setting, err := s.settings.Get(ctx, entity.SettingSendingPaused)
	if errors.Is(err, sql.ErrNoRows) {
		return SendingState{Paused: false}, nil
	}
	if err != nil {
		return SendingState{}, err
	}
	var v sendingPausedValue
	if err := json.Unmarshal(setting.Value, &v); err != nil {
		s.log.WithError(err).Warn("reputation: unreadable sending_paused value; treating as paused")
		return SendingState{Paused: true}, nil
	}
	return SendingState{Paused: true, Reason: v.Reason, Since: v.Since, By: v.By}, nil
}

// SendingPaused reports whether sending is paused. It satisfies the worker's
// SettingsReader so the dispatcher can read the pause flag through the circuit
// breaker (the dispatcher caches the result for a few seconds).
func (s *ReputationService) SendingPaused(ctx context.Context) (bool, error) {
	state, err := s.State(ctx)
	if err != nil {
		return false, err
	}
	return state.Paused, nil
}

// pct renders a fraction as a compact percentage string (0.043 → "4.3%",
// 0.04 → "4%", 0.0008 → "0.08%") for human-readable pause reasons.
func pct(frac float64) string {
	return strconv.FormatFloat(frac*100, 'f', -1, 64) + "%"
}
