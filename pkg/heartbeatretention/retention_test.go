package heartbeatretention

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeStore struct {
	mu      sync.Mutex
	results []state.ComputeNodeHeartbeatMaintenanceResult
	err     error
	calls   int
}

func (f *fakeStore) MaintainComputeNodeHeartbeatHistory(_ context.Context, _ time.Time, _ int) (state.ComputeNodeHeartbeatMaintenanceResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return state.ComputeNodeHeartbeatMaintenanceResult{}, f.err
	}
	if len(f.results) == 0 {
		return state.ComputeNodeHeartbeatMaintenanceResult{}, nil
	}
	r := f.results[0]
	f.results = f.results[1:]
	return r, nil
}

func TestSweepOnceDrainsBoundedBatchesAndPublishesHealth(t *testing.T) {
	now := time.Date(2026, 9, 13, 15, 0, 0, 0, time.UTC)
	store := &fakeStore{results: []state.ComputeNodeHeartbeatMaintenanceResult{
		{Deleted: 2, RollupBuckets: 1, OldestRawAt: now.Add(-8 * 24 * time.Hour)},
		{Deleted: 1, RollupBuckets: 1, OldestRawAt: now.Add(-2 * time.Hour)},
	}}
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "schedd")
	cleanup := New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), metrics).
		WithClock(func() time.Time { return now }).
		WithPolicy(7*24*time.Hour, time.Hour, 2, 4)

	result, err := cleanup.SweepOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 3 || result.RollupBuckets != 2 || store.calls != 2 {
		t.Fatalf("result=%+v calls=%d, want 3 rows/2 buckets/2 batches", result, store.calls)
	}
	if got := testutil.ToFloat64(metrics.deleted); got != 3 {
		t.Fatalf("deleted metric=%v, want 3", got)
	}
	if got := testutil.ToFloat64(metrics.oldestAge); got != (2 * time.Hour).Seconds() {
		t.Fatalf("oldest age=%v, want %v", got, (2 * time.Hour).Seconds())
	}
	if got := testutil.ToFloat64(metrics.lastSuccess); got != float64(now.Unix()) {
		t.Fatalf("last success=%v, want %d", got, now.Unix())
	}
}

func TestSweepFailureIsVisibleAndDoesNotClaimSuccess(t *testing.T) {
	store := &fakeStore{err: errors.New("database unavailable")}
	metrics := NewMetrics(prometheus.NewRegistry(), "schedd")
	cleanup := New(store, nil, metrics)
	if _, err := cleanup.SweepOnce(t.Context()); err == nil {
		t.Fatal("expected error")
	}
	if got := testutil.ToFloat64(metrics.failures); got != 1 {
		t.Fatalf("failures=%v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.lastSuccess); got != 0 {
		t.Fatalf("last success=%v, want 0 after failed first pass", got)
	}
}

func TestRunStopsWithContext(t *testing.T) {
	store := &fakeStore{}
	cleanup := New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), nil).
		WithPolicy(time.Hour, 5*time.Millisecond, 10, 1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		cleanup.Run(ctx)
		close(done)
	}()
	time.Sleep(12 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
}
