package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// fakeSettingsStore is an in-memory SettingsStore.
type fakeSettingsStore struct {
	rows       map[string]*entity.Setting
	upsertErr  error
	deleteErr  error
	getErr     error
	deleteSeen bool
}

func newFakeSettingsStore() *fakeSettingsStore {
	return &fakeSettingsStore{rows: map[string]*entity.Setting{}}
}

func (f *fakeSettingsStore) Get(_ context.Context, key string) (*entity.Setting, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	s, ok := f.rows[key]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return s, nil
}

func (f *fakeSettingsStore) Upsert(_ context.Context, key string, value json.RawMessage, now time.Time) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.rows[key] = &entity.Setting{Key: key, Value: append(json.RawMessage(nil), value...), UpdatedAt: now}
	return nil
}

func (f *fakeSettingsStore) Delete(_ context.Context, key string) error {
	f.deleteSeen = true
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.rows, key)
	return nil
}

// fakeReputationStore is a static ReputationStore recording the cut-off it saw.
type fakeReputationStore struct {
	sent       int
	hard       int
	complaints int
	err        error
	sinceSeen  time.Time
}

func (f *fakeReputationStore) CountSentRecipientsSince(_ context.Context, since time.Time) (int, error) {
	f.sinceSeen = since
	return f.sent, f.err
}

func (f *fakeReputationStore) CountHardBouncesAndComplaintsSince(_ context.Context, _ time.Time) (int, int, error) {
	return f.hard, f.complaints, f.err
}

func newReputation(settings *fakeSettingsStore, stats *fakeReputationStore) *ReputationService {
	svc := NewReputationService(settings, stats, ReputationConfig{
		BounceThreshold:    0.04,
		ComplaintThreshold: 0.0008,
		MinSample:          50,
	}, nil)
	svc.now = func() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) }
	return svc
}

func TestComputeRates(t *testing.T) {
	settings := newFakeSettingsStore()
	stats := &fakeReputationStore{sent: 200, hard: 10, complaints: 1}
	svc := newReputation(settings, stats)

	sent, hard, complaints, bounceRate, complaintRate, err := svc.ComputeRates(context.Background(), ReputationWindow)
	require.NoError(t, err)
	assert.Equal(t, 200, sent)
	assert.Equal(t, 10, hard)
	assert.Equal(t, 1, complaints)
	assert.InDelta(t, 0.05, bounceRate, 1e-9)
	assert.InDelta(t, 0.005, complaintRate, 1e-9)

	want := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC).Add(-ReputationWindow)
	assert.Equal(t, want, stats.sinceSeen, "cut-off is now - window")
}

func TestComputeRatesZeroSentNoDivision(t *testing.T) {
	svc := newReputation(newFakeSettingsStore(), &fakeReputationStore{sent: 0, hard: 3})
	sent, _, _, bounceRate, complaintRate, err := svc.ComputeRates(context.Background(), ReputationWindow)
	require.NoError(t, err)
	assert.Zero(t, sent)
	assert.Zero(t, bounceRate)
	assert.Zero(t, complaintRate)
}

func TestEvaluateBelowThresholdsDoesNotPause(t *testing.T) {
	settings := newFakeSettingsStore()
	svc := newReputation(settings, &fakeReputationStore{sent: 100, hard: 1}) // 1% < 4%
	svc.Evaluate(context.Background())

	st, _ := svc.State(context.Background())
	assert.False(t, st.Paused)
}

func TestEvaluateBounceOverPauses(t *testing.T) {
	settings := newFakeSettingsStore()
	svc := newReputation(settings, &fakeReputationStore{sent: 100, hard: 5}) // 5% >= 4%
	svc.Evaluate(context.Background())

	st, _ := svc.State(context.Background())
	require.True(t, st.Paused)
	assert.Equal(t, ActorAuto, st.By)
	assert.Contains(t, st.Reason, "bounce")
	assert.Contains(t, st.Reason, "5% >= 4%")
}

func TestEvaluateComplaintOverPauses(t *testing.T) {
	settings := newFakeSettingsStore()
	// bounce 0% (< 4%), complaint 0.1% (>= 0.08%)
	svc := newReputation(settings, &fakeReputationStore{sent: 1000, hard: 0, complaints: 1})
	svc.Evaluate(context.Background())

	st, _ := svc.State(context.Background())
	require.True(t, st.Paused)
	assert.Contains(t, st.Reason, "complaint")
}

func TestEvaluateMinSampleGuard(t *testing.T) {
	settings := newFakeSettingsStore()
	// 5/10 = 50% bounce rate, but only 10 sent (< 50 min sample).
	svc := newReputation(settings, &fakeReputationStore{sent: 10, hard: 5})
	svc.Evaluate(context.Background())

	st, _ := svc.State(context.Background())
	assert.False(t, st.Paused, "too little traffic to judge → no pause")
}

func TestEvaluateAlreadyPausedIsNoOp(t *testing.T) {
	settings := newFakeSettingsStore()
	settings.rows[entity.SettingSendingPaused] = &entity.Setting{
		Key:   entity.SettingSendingPaused,
		Value: json.RawMessage(`{"reason":"manual","since":"2026-08-23T09:00:00Z","by":"ops"}`),
	}
	svc := newReputation(settings, &fakeReputationStore{sent: 100, hard: 5}) // over threshold
	svc.Evaluate(context.Background())

	st, _ := svc.State(context.Background())
	require.True(t, st.Paused)
	assert.Equal(t, "manual", st.Reason, "an existing pause is not overwritten")
	assert.Equal(t, "ops", st.By)
}

func TestPauseThenStateRoundTrip(t *testing.T) {
	settings := newFakeSettingsStore()
	svc := newReputation(settings, &fakeReputationStore{})

	require.NoError(t, svc.Pause(context.Background(), "manual", "ops"))
	st, err := svc.State(context.Background())
	require.NoError(t, err)
	assert.True(t, st.Paused)
	assert.Equal(t, "manual", st.Reason)
	assert.Equal(t, "ops", st.By)
	assert.Equal(t, time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC), st.Since)
}

func TestResumeClearsRow(t *testing.T) {
	settings := newFakeSettingsStore()
	svc := newReputation(settings, &fakeReputationStore{})
	require.NoError(t, svc.Pause(context.Background(), "manual", "ops"))

	require.NoError(t, svc.Resume(context.Background(), "ops"))
	assert.True(t, settings.deleteSeen)
	st, _ := svc.State(context.Background())
	assert.False(t, st.Paused, "resume clears the pause row")
}

func TestSendingPausedReflectsState(t *testing.T) {
	settings := newFakeSettingsStore()
	svc := newReputation(settings, &fakeReputationStore{})

	paused, err := svc.SendingPaused(context.Background())
	require.NoError(t, err)
	assert.False(t, paused)

	require.NoError(t, svc.Pause(context.Background(), "manual", "ops"))
	paused, err = svc.SendingPaused(context.Background())
	require.NoError(t, err)
	assert.True(t, paused)
}
