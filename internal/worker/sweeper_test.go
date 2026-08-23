package worker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGate is a dispatchGate with controllable pause and back-pressure.
type fakeGate struct {
	paused   bool
	accept   int // remaining Enqueue calls that succeed; negative = unlimited
	enqueued []string
	inflight []string
}

func (g *fakeGate) Enqueue(id string) bool {
	if g.accept == 0 {
		return false // channel full
	}
	if g.accept > 0 {
		g.accept--
	}
	g.enqueued = append(g.enqueued, id)
	return true
}
func (g *fakeGate) Paused() bool               { return g.paused }
func (g *fakeGate) inflightSnapshot() []string { return g.inflight }

// sweeperStore records FindDueQueued/UnstickStaleSending activity and returns
// successive due batches.
type sweeperStore struct {
	stubStore
	batches      [][]string
	call         int
	excludeSeen  [][]string
	unstickCalls int
	unstickArg   time.Time
}

func (s *sweeperStore) FindDueQueued(_ context.Context, _ time.Time, _ int, exclude []string) ([]string, error) {
	s.excludeSeen = append(s.excludeSeen, exclude)
	if s.call >= len(s.batches) {
		return nil, nil
	}
	b := s.batches[s.call]
	s.call++
	return b, nil
}

func (s *sweeperStore) UnstickStaleSending(_ context.Context, olderThan, _ time.Time) (int64, error) {
	s.unstickCalls++
	s.unstickArg = olderThan
	return 0, nil
}

func newSweeper(store MessageStore, gate dispatchGate, batch int) *Sweeper {
	s := NewSweeper(store, gate, SweeperConfig{Interval: time.Hour, StaleLockAfter: 5 * time.Minute, BatchSize: batch}, nil)
	s.clock = func() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) }
	return s
}

func TestSweepEnqueuesDueRows(t *testing.T) {
	store := &sweeperStore{batches: [][]string{{"a", "b"}}} // short batch (< 100) → one pass
	gate := &fakeGate{accept: -1}
	newSweeper(store, gate, 100).Sweep(context.Background())

	assert.Equal(t, []string{"a", "b"}, gate.enqueued)
	assert.Equal(t, 1, store.call, "a short batch stops the drain")
	assert.Equal(t, 1, store.unstickCalls, "stale recovery runs every sweep")
}

func TestSweepDrainsMultipleFullBatches(t *testing.T) {
	// Batch size 2: first batch is full (keep draining), second is short (stop).
	store := &sweeperStore{batches: [][]string{{"a", "b"}, {"c"}}}
	gate := &fakeGate{accept: -1}
	newSweeper(store, gate, 2).Sweep(context.Background())

	assert.Equal(t, []string{"a", "b", "c"}, gate.enqueued)
	assert.Equal(t, 2, store.call, "the drain loops while batches are full")
}

func TestSweepStopsWhenChannelFull(t *testing.T) {
	store := &sweeperStore{batches: [][]string{{"a", "b", "c"}}}
	gate := &fakeGate{accept: 1} // accepts a, rejects b
	newSweeper(store, gate, 100).Sweep(context.Background())

	assert.Equal(t, []string{"a"}, gate.enqueued, "drain stops as soon as the pool is saturated")
	assert.Equal(t, 1, store.call)
}

func TestSweepExcludesInflight(t *testing.T) {
	store := &sweeperStore{batches: [][]string{{"a"}}}
	gate := &fakeGate{accept: -1, inflight: []string{"x", "y"}}
	newSweeper(store, gate, 100).Sweep(context.Background())

	require.Len(t, store.excludeSeen, 1)
	assert.ElementsMatch(t, []string{"x", "y"}, store.excludeSeen[0], "in-flight ids are excluded from the query")
}

func TestSweepUnsticksStaleWithThreshold(t *testing.T) {
	store := &sweeperStore{}
	gate := &fakeGate{accept: -1}
	newSweeper(store, gate, 100).Sweep(context.Background())

	require.Equal(t, 1, store.unstickCalls)
	want := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC).Add(-5 * time.Minute)
	assert.Equal(t, want, store.unstickArg, "stale threshold is now - STALE_LOCK_AFTER")
}

func TestSweepNothingEnqueuedWhilePaused(t *testing.T) {
	store := &sweeperStore{batches: [][]string{{"a", "b"}}}
	gate := &fakeGate{accept: -1, paused: true}
	newSweeper(store, gate, 100).Sweep(context.Background())

	assert.Empty(t, gate.enqueued, "paused → nothing dispatched")
	assert.Equal(t, 0, store.call, "paused → no due-row query")
	assert.Equal(t, 1, store.unstickCalls, "stale recovery still runs while paused")
}
