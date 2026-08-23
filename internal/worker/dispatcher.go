package worker

import (
	"context"
	"sync"
	"time"

	logrus "github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

// processFunc is the piece of the processor the dispatcher drives. Defining it
// as an interface breaks the construction cycle (dispatcher ⇄ processor) and
// lets tests inject a fake.
type processFunc interface {
	Process(ctx context.Context, id string)
}

// DispatcherConfig configures the worker pool.
type DispatcherConfig struct {
	Concurrency int           // number of sender goroutines (WORKER_CONCURRENCY)
	BufferSize  int           // in-memory hand-off channel size (DISPATCH_BUFFER)
	MaxSendRate float64       // messages/sec across the pool (PROVIDER_MAX_SEND_RATE); <=0 disables the limit
	SendTimeout time.Duration // per-send deadline, independent of shutdown (default 30s)
	PauseTTL    time.Duration // how long a sending_paused read is cached (default 5s)
}

// Dispatcher is the in-process worker pool. Enqueue offers a message id to a
// bounded, buffered channel that WORKER_CONCURRENCY goroutines drain, claim and
// send. The bound is deliberate: throughput scales with concurrency/instances,
// never by spawning a goroutine per email (Data model — "Throughput and the
// bounded pool"). MySQL remains the source of truth; a dropped hand-off is
// recovered by the sweeper.
type Dispatcher struct {
	ch          chan string
	processor   processFunc
	limiter     *rate.Limiter
	settings    SettingsReader
	log         logrus.FieldLogger
	clock       func() time.Time
	concurrency int
	sendTimeout time.Duration

	pauseTTL     time.Duration
	pauseMu      sync.Mutex
	pausedVal    bool
	pausedAt     time.Time
	pauseFetched bool

	inflightMu sync.Mutex
	inflight   map[string]struct{}

	quit     chan struct{}
	quitOnce sync.Once
	wg       sync.WaitGroup
}

// NewDispatcher builds a Dispatcher. Call SetProcessor before Start. settings
// may be nil (treated as never paused), which is convenient in tests.
func NewDispatcher(cfg DispatcherConfig, settings SettingsReader, logger logrus.FieldLogger) *Dispatcher {
	if cfg.SendTimeout <= 0 {
		cfg.SendTimeout = 30 * time.Second
	}
	if cfg.PauseTTL <= 0 {
		cfg.PauseTTL = 5 * time.Second
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.BufferSize < 1 {
		cfg.BufferSize = 1
	}
	limit := rate.Inf
	burst := 1
	if cfg.MaxSendRate > 0 {
		limit = rate.Limit(cfg.MaxSendRate)
		if b := int(cfg.MaxSendRate); b > burst {
			burst = b
		}
	}
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	return &Dispatcher{
		ch:          make(chan string, cfg.BufferSize),
		limiter:     rate.NewLimiter(limit, burst),
		settings:    settings,
		log:         logger,
		clock:       time.Now,
		concurrency: cfg.Concurrency,
		sendTimeout: cfg.SendTimeout,
		pauseTTL:    cfg.PauseTTL,
		inflight:    make(map[string]struct{}),
		quit:        make(chan struct{}),
	}
}

// SetProcessor wires the processor that workers invoke. Must be called before
// Start.
func (d *Dispatcher) SetProcessor(p processFunc) { d.processor = p }

// Enqueue offers id to the worker pool without ever blocking. It returns true
// when the id was accepted (buffered) or is already pending, false when the
// buffer is full — in which case the sweeper will pick the row up from the
// database. This keeps the HTTP accept path fast under any load.
func (d *Dispatcher) Enqueue(id string) bool {
	d.inflightMu.Lock()
	defer d.inflightMu.Unlock()
	if _, pending := d.inflight[id]; pending {
		return true // already buffered or processing; do not double-enqueue
	}
	select {
	case d.ch <- id:
		d.inflight[id] = struct{}{}
		return true
	default:
		return false
	}
}

// Start launches the worker goroutines. ctx cancellation initiates a graceful
// stop (equivalent to calling Stop).
func (d *Dispatcher) Start(ctx context.Context) {
	for i := 0; i < d.concurrency; i++ {
		d.wg.Add(1)
		go d.worker()
	}
	go func() {
		select {
		case <-ctx.Done():
			d.signalStop()
		case <-d.quit:
		}
	}()
}

// Stop initiates a graceful shutdown: workers stop claiming new messages and
// in-flight sends are given up to timeout to finish. It reports whether the
// pool drained cleanly within the timeout.
func (d *Dispatcher) Stop(timeout time.Duration) bool {
	d.signalStop()
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		d.log.Warn("worker pool did not drain within shutdown timeout")
		return false
	}
}

func (d *Dispatcher) signalStop() {
	d.quitOnce.Do(func() { close(d.quit) })
}

// worker pulls ids and processes them until a stop is signalled. It checks quit
// before each pull so that once shutdown begins no new message is claimed,
// while a send already in progress is allowed to finish.
func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for {
		select {
		case <-d.quit:
			return
		default:
		}
		select {
		case <-d.quit:
			return
		case id := <-d.ch:
			d.handle(id)
		}
	}
}

// handle processes one id: it skips work while paused (the sweeper re-dispatches
// on resume), waits for a rate-limit token, then runs the processor with a
// send-scoped context that outlives shutdown so in-flight sends complete.
func (d *Dispatcher) handle(id string) {
	defer d.release(id)
	if d.Paused() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.sendTimeout)
	defer cancel()
	if err := d.limiter.Wait(ctx); err != nil {
		return
	}
	if d.processor != nil {
		d.processor.Process(ctx, id)
	}
}

func (d *Dispatcher) release(id string) {
	d.inflightMu.Lock()
	delete(d.inflight, id)
	d.inflightMu.Unlock()
}

// Paused reports whether global sending is paused, caching the settings read
// for PauseTTL so the hot path does not hit the database on every message.
func (d *Dispatcher) Paused() bool {
	if d.settings == nil {
		return false
	}
	d.pauseMu.Lock()
	defer d.pauseMu.Unlock()
	now := d.clock()
	if !d.pauseFetched || now.Sub(d.pausedAt) >= d.pauseTTL {
		paused, err := d.settings.SendingPaused(context.Background())
		if err != nil {
			d.log.WithError(err).Warn("reading sending_paused; assuming not paused")
			paused = false
		}
		d.pausedVal = paused
		d.pausedAt = now
		d.pauseFetched = true
	}
	return d.pausedVal
}

// inflightSnapshot returns the ids currently buffered or processing, so the
// sweeper can skip them when draining the backlog.
func (d *Dispatcher) inflightSnapshot() []string {
	d.inflightMu.Lock()
	defer d.inflightMu.Unlock()
	out := make([]string, 0, len(d.inflight))
	for id := range d.inflight {
		out = append(out, id)
	}
	return out
}

// NoopDispatcher is a Dispatcher that accepts nothing. It backs `serve
// --no-worker` (API-only) mode and is convenient in tests: the accept path
// still stores messages, and — when a worker instance runs elsewhere — the
// sweeper drains them.
type NoopDispatcher struct{}

// Enqueue always reports the id was dropped.
func (NoopDispatcher) Enqueue(string) bool { return false }
