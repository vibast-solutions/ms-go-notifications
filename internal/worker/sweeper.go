package worker

import (
	"context"
	"time"

	logrus "github.com/sirupsen/logrus"
)

// defaultSweepBatch is how many due rows the sweeper claims per query while
// draining the backlog.
const defaultSweepBatch = 100

// reputationInterval is how often the reputation hook runs (Business Logic §8).
// The evaluation itself is added in VIB-11; this story only wires the cadence.
const reputationInterval = 60 * time.Second

// dispatchGate is the slice of the dispatcher the sweeper drives: offer ids,
// read the pause flag, and learn which ids are already in flight.
type dispatchGate interface {
	Enqueue(id string) bool
	Paused() bool
	inflightSnapshot() []string
}

// SweeperConfig configures the sweeper.
type SweeperConfig struct {
	Interval       time.Duration // SWEEP_INTERVAL
	StaleLockAfter time.Duration // STALE_LOCK_AFTER
	BatchSize      int           // rows per drain query (default 100)
}

// Sweeper makes MySQL the durable queue. On start and every Interval it drains
// queued/due rows into the worker pool and recovers rows stuck in 'sending'
// after a crash. It guarantees nothing is lost across restarts or bursts: the
// cost of a backlog is latency, never a dropped message.
type Sweeper struct {
	store      MessageStore
	dispatcher dispatchGate
	cfg        SweeperConfig
	log        logrus.FieldLogger
	clock      func() time.Time

	lastReputation time.Time
	// reputation is the circuit-breaker evaluation hook. It is nil in this
	// story and wired in VIB-11; the call site below is intentionally named.
	reputation func(ctx context.Context)
}

// NewSweeper wires a Sweeper. dispatcher is the concrete *Dispatcher in
// production; tests may pass a fake implementing dispatchGate.
func NewSweeper(store MessageStore, dispatcher dispatchGate, cfg SweeperConfig, logger logrus.FieldLogger) *Sweeper {
	if cfg.BatchSize < 1 {
		cfg.BatchSize = defaultSweepBatch
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	return &Sweeper{
		store:      store,
		dispatcher: dispatcher,
		cfg:        cfg,
		log:        logger,
		clock:      time.Now,
	}
}

// Run sweeps once immediately (so a restart re-dispatches queued rows) and then
// every Interval until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) {
	s.Sweep(ctx)
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Sweep(ctx)
		}
	}
}

// Sweep performs one pass: drain the queued backlog (unless paused), then
// recover stale 'sending' rows, then run the reputation hook if due.
func (s *Sweeper) Sweep(ctx context.Context) {
	now := s.clock().UTC().Truncate(time.Millisecond)

	if !s.dispatcher.Paused() {
		s.drain(ctx, now)
	}

	threshold := now.Add(-s.cfg.StaleLockAfter)
	if n, err := s.store.UnstickStaleSending(ctx, threshold, now); err != nil {
		s.log.WithError(err).Error("unsticking stale sending rows")
	} else if n > 0 {
		s.log.WithField("count", n).Info("recovered stale sending messages")
	}

	s.maybeEvaluateReputation(ctx, now)
}

// drain repeatedly claims batches of due rows and enqueues them, looping until
// a batch is short (backlog empty) or the pool is saturated (Enqueue returns
// false) — in which case it resumes next tick. In-flight ids are excluded so
// the same row is not offered twice.
func (s *Sweeper) drain(ctx context.Context, now time.Time) {
	for {
		exclude := s.dispatcher.inflightSnapshot()
		ids, err := s.store.FindDueQueued(ctx, now, s.cfg.BatchSize, exclude)
		if err != nil {
			s.log.WithError(err).Error("finding due queued messages")
			return
		}
		for _, id := range ids {
			if !s.dispatcher.Enqueue(id) {
				return // workers saturated; next tick continues the drain
			}
		}
		if len(ids) < s.cfg.BatchSize {
			return // backlog drained
		}
	}
}

// maybeEvaluateReputation runs the reputation hook at most once per
// reputationInterval. The evaluation is implemented in VIB-11.
func (s *Sweeper) maybeEvaluateReputation(ctx context.Context, now time.Time) {
	if !s.lastReputation.IsZero() && now.Sub(s.lastReputation) < reputationInterval {
		return
	}
	s.lastReputation = now
	if s.reputation != nil {
		s.reputation(ctx)
	}
}
