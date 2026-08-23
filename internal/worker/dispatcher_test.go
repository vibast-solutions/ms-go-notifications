package worker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueFullChannelReturnsFalseWithoutBlocking(t *testing.T) {
	d := NewDispatcher(DispatcherConfig{Concurrency: 1, BufferSize: 2}, nil, nil)
	// No workers started, so nothing drains the channel.
	assert.True(t, d.Enqueue("a"))
	assert.True(t, d.Enqueue("b"))

	// The third enqueue must return false immediately, never block.
	done := make(chan bool, 1)
	go func() { done <- d.Enqueue("c") }()
	select {
	case got := <-done:
		assert.False(t, got, "enqueue on a full channel returns false")
	case <-time.After(time.Second):
		t.Fatal("Enqueue blocked on a full channel")
	}
}

func TestEnqueueDeduplicates(t *testing.T) {
	d := NewDispatcher(DispatcherConfig{Concurrency: 1, BufferSize: 8}, nil, nil)
	assert.True(t, d.Enqueue("a"))
	assert.True(t, d.Enqueue("a"), "a duplicate is accepted as already-pending")
	assert.Len(t, d.inflightSnapshot(), 1, "duplicate must not be tracked twice")

	require.Equal(t, "a", <-d.ch)
	select {
	case id := <-d.ch:
		t.Fatalf("duplicate id %q was enqueued twice", id)
	default:
	}
}

func TestPausedIsCached(t *testing.T) {
	fs := &fakeSettings{paused: true}
	d := NewDispatcher(DispatcherConfig{Concurrency: 1, BufferSize: 1, PauseTTL: 5 * time.Second}, fs, nil)

	now := time.Now()
	d.clock = func() time.Time { return now }

	assert.True(t, d.Paused())
	assert.True(t, d.Paused())
	assert.Equal(t, 1, fs.callCount(), "reads within the TTL are served from cache")

	// Advance past the TTL → one more read.
	now = now.Add(6 * time.Second)
	assert.True(t, d.Paused())
	assert.Equal(t, 2, fs.callCount())
}

func TestWorkersSkipClaimWhilePaused(t *testing.T) {
	fs := &fakeSettings{paused: true}
	d := NewDispatcher(DispatcherConfig{Concurrency: 1, BufferSize: 4}, fs, nil)
	fp := &fakeProcessor{}
	d.SetProcessor(fp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Start(ctx)

	require.True(t, d.Enqueue("a"))
	// The worker pulls the id, sees paused, drops it and releases the inflight
	// slot without ever invoking the processor.
	waitFor(t, time.Second, func() bool { return len(d.inflightSnapshot()) == 0 })
	assert.Empty(t, fp.processed(), "paused pool must not claim/process")

	d.Stop(time.Second)
}

func TestGracefulStopWaitsForInflight(t *testing.T) {
	d := NewDispatcher(DispatcherConfig{Concurrency: 1, BufferSize: 4}, nil, nil)
	block := make(chan struct{})
	fp := &fakeProcessor{block: block}
	d.SetProcessor(fp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Start(ctx)

	require.True(t, d.Enqueue("a"))
	waitFor(t, time.Second, func() bool { return len(fp.processed()) == 1 }) // processing started

	stopped := make(chan bool, 1)
	go func() { stopped <- d.Stop(2 * time.Second) }()

	// Stop is blocked on the in-flight send; releasing it lets the pool drain.
	close(block)
	select {
	case ok := <-stopped:
		assert.True(t, ok, "pool drained cleanly once the in-flight send finished")
	case <-time.After(2 * time.Second):
		t.Fatal("Stop hung after in-flight send completed")
	}
}

func TestNoopDispatcher(t *testing.T) {
	assert.False(t, NoopDispatcher{}.Enqueue("anything"))
}
