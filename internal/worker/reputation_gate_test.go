package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

// memSettings is an in-memory service.SettingsStore backing the reputation
// service in the integration test.
type memSettings struct {
	mu   sync.Mutex
	rows map[string]*entity.Setting
}

func (m *memSettings) Get(_ context.Context, key string) (*entity.Setting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.rows[key]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return s, nil
}

func (m *memSettings) Upsert(_ context.Context, key string, value json.RawMessage, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[key] = &entity.Setting{Key: key, Value: append(json.RawMessage(nil), value...), UpdatedAt: now}
	return nil
}

func (m *memSettings) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, key)
	return nil
}

// zeroRepStore reports no traffic; the test drives pause/resume through the
// service directly rather than through automatic evaluation.
type zeroRepStore struct{}

func (zeroRepStore) CountSentRecipientsSince(context.Context, time.Time) (int, error) { return 0, nil }
func (zeroRepStore) CountHardBouncesAndComplaintsSince(context.Context, time.Time) (int, int, error) {
	return 0, 0, nil
}

// TestReputationGateStopsAndResumesSending wires the real reputation service as
// the dispatcher's pause source (as serve does) and proves the end-to-end gate:
// while paused via the service, neither the dispatcher nor the sweeper claims a
// queued row; after a manual resume the sweeper drains the backlog.
func TestReputationGateStopsAndResumesSending(t *testing.T) {
	ids := []string{"a", "b", "c"}
	store := newMemStore(ids)
	prov := &countingProvider{perID: make(map[string]int)}

	reputation := service.NewReputationService(
		&memSettings{rows: map[string]*entity.Setting{}},
		zeroRepStore{},
		service.ReputationConfig{BounceThreshold: 0.04, ComplaintThreshold: 0.0008, MinSample: 50},
		nil,
	)

	// Pause before anything starts.
	require.NoError(t, reputation.Pause(context.Background(), "manual", "ops"))

	// Short PauseTTL so the dispatcher's cache reflects resume quickly.
	disp := NewDispatcher(DispatcherConfig{Concurrency: 2, BufferSize: 16, PauseTTL: 20 * time.Millisecond}, reputation, nil)
	proc := NewProcessor(store, noopEvents{}, prov, disp, ProcessorConfig{MaxAttempts: 5, BackoffBase: time.Second}, nil)
	disp.SetProcessor(proc)
	sweep := NewSweeper(store, disp, SweeperConfig{Interval: 5 * time.Millisecond, StaleLockAfter: time.Hour, BatchSize: 100}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	disp.Start(ctx)
	go sweep.Run(ctx)

	// While paused, several sweep cycles must claim/send nothing.
	time.Sleep(80 * time.Millisecond)
	assert.Zero(t, store.sentCount(), "paused → dispatcher/sweeper claim nothing")

	// Resume: the sweeper picks the queued backlog up.
	require.NoError(t, reputation.Resume(context.Background(), "ops"))
	waitFor(t, 5*time.Second, func() bool { return store.sentCount() == len(ids) })

	cancel()
	disp.Stop(2 * time.Second)
	assert.Equal(t, len(ids), prov.total, "all queued rows sent after resume")
	assert.Equal(t, 1, prov.maxPerID(), "no double-send")
}
