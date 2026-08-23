package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// fakeSettings is a SettingsReader with a controllable pause flag and a call
// counter (to assert caching).
type fakeSettings struct {
	mu     sync.Mutex
	paused bool
	calls  int
}

func (f *fakeSettings) SendingPaused(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.paused, nil
}

func (f *fakeSettings) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeProcessor records the ids it processed and can block to simulate an
// in-flight send.
type fakeProcessor struct {
	mu    sync.Mutex
	ids   []string
	block chan struct{}
}

func (f *fakeProcessor) Process(_ context.Context, id string) {
	f.mu.Lock()
	f.ids = append(f.ids, id)
	f.mu.Unlock()
	if f.block != nil {
		<-f.block
	}
}

func (f *fakeProcessor) processed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ids...)
}

// noopEvents is an EventInserter that discards everything.
type noopEvents struct{}

func (noopEvents) Insert(context.Context, *entity.MessageEvent) error { return nil }

// waitFor polls cond until it is true or the timeout elapses, failing the test
// on timeout. It keeps timing-sensitive tests from flaking on scheduling.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
