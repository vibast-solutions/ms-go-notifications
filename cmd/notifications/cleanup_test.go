package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveCleanupArgs(t *testing.T) {
	cases := []struct {
		name       string
		target     string
		olderThan  time.Duration
		olderSet   bool
		batch      int
		wantAge    time.Duration
		wantErrSub string
	}{
		{name: "attachments default", target: "attachments", batch: 1000, wantAge: defaultAttachmentsAge},
		{name: "history default", target: "history", batch: 1000, wantAge: defaultHistoryAge},
		{name: "explicit age", target: "attachments", olderThan: 48 * time.Hour, olderSet: true, batch: 1000, wantAge: 48 * time.Hour},
		{name: "target required", target: "", batch: 1000, wantErrSub: "required"},
		{name: "unknown target", target: "everything", batch: 1000, wantErrSub: "unknown --target"},
		{name: "below floor", target: "history", olderThan: 12 * time.Hour, olderSet: true, batch: 1000, wantErrSub: "at least 24h"},
		{name: "floor exactly 24h ok", target: "history", olderThan: 24 * time.Hour, olderSet: true, batch: 1000, wantAge: 24 * time.Hour},
		{name: "bad batch", target: "history", batch: 0, wantErrSub: "batch must be positive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			age, err := resolveCleanupArgs(tc.target, tc.olderThan, tc.olderSet, tc.batch)
			if tc.wantErrSub != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrSub)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantAge, age)
		})
	}
}

// fakeCleanupRepo returns a scripted sequence of batch counts.
type fakeCleanupRepo struct {
	purge   []int64
	history []int64
	pCall   int
	hCall   int
}

func (f *fakeCleanupRepo) PurgeAttachmentsBatch(_ context.Context, _ time.Time, _ int, _ time.Time) (int64, error) {
	n := f.purge[f.pCall]
	f.pCall++
	return n, nil
}

func (f *fakeCleanupRepo) DeleteHistoryBatch(_ context.Context, _ time.Time, _ int) (int64, error) {
	n := f.history[f.hCall]
	f.hCall++
	return n, nil
}

func TestRunCleanupLoopBatchesUntilShort(t *testing.T) {
	repo := &fakeCleanupRepo{purge: []int64{1000, 1000, 250}} // two full batches then a short one
	var out bytes.Buffer
	total, err := runCleanupLoop(context.Background(), repo, cleanupTargetAttachments,
		time.Now(), time.Now(), 1000, &out)
	require.NoError(t, err)
	assert.Equal(t, int64(2250), total)
	assert.Equal(t, 3, repo.pCall, "loops until a batch is short")
}

func TestRunCleanupLoopHistorySingleShortBatch(t *testing.T) {
	repo := &fakeCleanupRepo{history: []int64{5}} // fewer than batch → one pass
	var out bytes.Buffer
	total, err := runCleanupLoop(context.Background(), repo, cleanupTargetHistory,
		time.Now(), time.Now(), 1000, &out)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Equal(t, 1, repo.hCall)
}

// erroringRepo fails on the first batch.
type erroringRepo struct{}

func (erroringRepo) PurgeAttachmentsBatch(context.Context, time.Time, int, time.Time) (int64, error) {
	return 0, errors.New("db down")
}
func (erroringRepo) DeleteHistoryBatch(context.Context, time.Time, int) (int64, error) {
	return 0, errors.New("db down")
}

func TestRunCleanupLoopPropagatesError(t *testing.T) {
	var out bytes.Buffer
	_, err := runCleanupLoop(context.Background(), erroringRepo{}, cleanupTargetAttachments,
		time.Now(), time.Now(), 1000, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

func TestCleanupCommandRequiresTarget(t *testing.T) {
	cmd := newCleanupCmd()
	cmd.SetArgs([]string{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	require.Error(t, err, "target is a required flag")
	assert.Contains(t, err.Error(), "target")
}
