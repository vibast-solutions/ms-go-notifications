package worker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/pkg/provider"
)

// memStore is an in-memory MessageStore whose Claim enforces the same guard as
// the real UPDATE: only the first claimer of a 'queued' row wins. It lets the
// burst test prove that a large backlog is fully drained with no double-sends,
// without a database.
type memStore struct {
	stubStore
	mu       sync.Mutex
	ids      []string
	status   map[string]string
	attempts map[string]int
}

func newMemStore(ids []string) *memStore {
	m := &memStore{
		ids:      ids,
		status:   make(map[string]string, len(ids)),
		attempts: make(map[string]int, len(ids)),
	}
	for _, id := range ids {
		m.status[id] = entity.MessageStatusQueued
	}
	return m
}

func (m *memStore) Claim(_ context.Context, id, _ string, _ time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status[id] != entity.MessageStatusQueued {
		return false, nil // already claimed/sent — the guard rejects the double
	}
	m.status[id] = entity.MessageStatusSending
	m.attempts[id]++
	return true, nil
}

func (m *memStore) FindByID(_ context.Context, id string) (*entity.Message, error) {
	m.mu.Lock()
	attempts := m.attempts[id]
	m.mu.Unlock()
	text := "hello"
	return &entity.Message{
		ID: id, FromEmail: "no-reply@vibast.ro", FromDomain: "vibast.ro",
		Subject: "Hi", TextBody: &text, Attempts: attempts, Status: entity.MessageStatusSending,
	}, nil
}

func (m *memStore) LoadRecipients(_ context.Context, id string) ([]*entity.MessageRecipient, error) {
	return []*entity.MessageRecipient{
		{ID: id + "r", MessageID: id, Type: entity.RecipientTypeTo, Email: "jane@example.com", Status: entity.RecipientStatusPending},
	}, nil
}

func (m *memStore) MarkSent(_ context.Context, id, _, _ string, _ time.Time) error {
	m.mu.Lock()
	m.status[id] = entity.MessageStatusSent
	m.mu.Unlock()
	return nil
}

func (m *memStore) FindDueQueued(_ context.Context, _ time.Time, limit int, exclude []string) ([]string, error) {
	skip := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		skip[e] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, id := range m.ids {
		if m.status[id] == entity.MessageStatusQueued && !skip[id] {
			out = append(out, id)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *memStore) sentCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.status {
		if s == entity.MessageStatusSent {
			n++
		}
	}
	return n
}

// countingProvider counts sends per message id to detect any double-send.
type countingProvider struct {
	mu    sync.Mutex
	perID map[string]int
	total int
}

func (p *countingProvider) Name() string { return "count" }
func (p *countingProvider) Send(_ context.Context, msg *provider.OutboundEmail) (string, error) {
	p.mu.Lock()
	p.perID[msg.MessageID]++
	p.total++
	p.mu.Unlock()
	return "id-" + msg.MessageID, nil
}

func (p *countingProvider) maxPerID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	max := 0
	for _, c := range p.perID {
		if c > max {
			max = c
		}
	}
	return max
}

func TestBurstFiveThousandProcessedExactlyOnce(t *testing.T) {
	const n = 5000
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("%05d", i)
	}

	store := newMemStore(ids)
	prov := &countingProvider{perID: make(map[string]int)}

	disp := NewDispatcher(DispatcherConfig{Concurrency: 4, BufferSize: 256}, nil, nil)
	proc := NewProcessor(store, noopEvents{}, prov, disp, ProcessorConfig{MaxAttempts: 5, BackoffBase: time.Second}, nil)
	disp.SetProcessor(proc)
	sweep := NewSweeper(store, disp, SweeperConfig{Interval: 2 * time.Millisecond, StaleLockAfter: time.Hour, BatchSize: 100}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	disp.Start(ctx)
	go sweep.Run(ctx)

	waitFor(t, 30*time.Second, func() bool { return store.sentCount() == n })
	cancel()
	disp.Stop(2 * time.Second)

	assert.Equal(t, n, store.sentCount(), "every queued row is sent")
	assert.Equal(t, n, prov.total, "sent exactly n times in total — none lost")
	assert.Equal(t, 1, prov.maxPerID(), "no message double-sent (claim guard holds)")
}
