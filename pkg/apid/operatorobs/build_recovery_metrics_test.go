package operatorobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

type recoveryReadFunc func(context.Context) (state.BuildRecoverySnapshot, error)

func (f recoveryReadFunc) BuildRecoverySnapshot(ctx context.Context) (state.BuildRecoverySnapshot, error) {
	return f(ctx)
}

func TestBuildRecoveryMetricsLifecycle(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := NewBuildRecoveryMetrics(reg)
	now := time.Unix(1700000000, 0)
	m.now = func() time.Time { return now }
	checkMissing := func(wantSuccess, wantLast float64) {
		t.Helper()
		got := queueMetrics(t, reg)
		if len(got) != 2 || got["apid_build_recovery_collection_success"] != wantSuccess || got["apid_build_recovery_collection_last_success_timestamp_seconds"] != wantLast {
			t.Fatalf("unavailable observation=%v", got)
		}
	}
	checkMissing(0, 0)
	valid := state.BuildRecoverySnapshot{Running: 2, OldestStartedAt: now.Add(-time.Hour), PendingCleanup: 3, OldestCleanupCreatedAt: now.Add(-2 * time.Hour), RetriedCleanup: 1}
	read := recoveryReadFunc(func(ctx context.Context) (state.BuildRecoverySnapshot, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > api.ObsBuildRecoveryCollectionBudget {
			t.Fatal("missing read deadline")
		}
		return valid, nil
	})
	if err := m.refresh(t.Context(), read); err != nil {
		t.Fatal(err)
	}
	last := float64(now.Unix())
	got := queueMetrics(t, reg)
	if len(got) != 7 || got["faas_build_running_count"] != 2 || got["faas_build_running_oldest_started_timestamp_seconds"] != float64(valid.OldestStartedAt.Unix()) || got["faas_build_vm_cleanup_pending"] != 3 || got["faas_build_vm_cleanup_retry_pending"] != 1 || got["faas_build_vm_cleanup_oldest_created_timestamp_seconds"] != float64(valid.OldestCleanupCreatedAt.Unix()) {
		t.Fatalf("snapshot=%v", got)
	}
	for _, tc := range []struct {
		name     string
		snapshot state.BuildRecoverySnapshot
		err      error
	}{
		{"read failure", valid, errors.New("database unavailable")},
		{"missing running age", state.BuildRecoverySnapshot{Running: 1}, nil},
		{"missing cleanup age", state.BuildRecoverySnapshot{PendingCleanup: 1}, nil},
		{"empty with running age", state.BuildRecoverySnapshot{OldestStartedAt: now}, nil},
		{"empty with cleanup age", state.BuildRecoverySnapshot{OldestCleanupCreatedAt: now}, nil},
		{"negative running", state.BuildRecoverySnapshot{Running: -1}, nil},
		{"negative cleanup", state.BuildRecoverySnapshot{PendingCleanup: -1}, nil},
		{"negative retry", state.BuildRecoverySnapshot{RetriedCleanup: -1}, nil},
		{"retry exceeds pending", state.BuildRecoverySnapshot{RetriedCleanup: 1}, nil},
		{"future running", state.BuildRecoverySnapshot{Running: 1, OldestStartedAt: now.Add(time.Hour)}, nil},
		{"future cleanup", state.BuildRecoverySnapshot{PendingCleanup: 1, OldestCleanupCreatedAt: now.Add(time.Hour)}, nil},
		{"pre epoch", state.BuildRecoverySnapshot{Running: 1, OldestStartedAt: time.Unix(-1, 0)}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := m.refresh(t.Context(), recoveryReadFunc(func(context.Context) (state.BuildRecoverySnapshot, error) { return tc.snapshot, tc.err })); err == nil {
				t.Fatal("invalid observation accepted")
			}
			checkMissing(0, last)
		})
	}
	// A legitimate claim can be created during the bounded snapshot read.
	if err := m.refresh(t.Context(), recoveryReadFunc(func(context.Context) (state.BuildRecoverySnapshot, error) {
		now = now.Add(time.Second)
		return state.BuildRecoverySnapshot{Running: 1, OldestStartedAt: now}, nil
	})); err != nil {
		t.Fatalf("claim created during read rejected: %v", err)
	}
	now = now.Add(time.Minute)
	if err := m.refresh(t.Context(), recoveryReadFunc(func(context.Context) (state.BuildRecoverySnapshot, error) { return state.BuildRecoverySnapshot{}, nil })); err != nil {
		t.Fatal(err)
	}
	got = queueMetrics(t, reg)
	if len(got) != 7 || got["faas_build_running_count"] != 0 || got["faas_build_vm_cleanup_pending"] != 0 || got["faas_build_vm_cleanup_oldest_created_timestamp_seconds"] != 0 {
		t.Fatalf("empty=%v", got)
	}
	last = float64(now.Unix())
	now = now.Add(api.ObsBuildRecoveryFreshnessMaxAge + time.Nanosecond)
	checkMissing(1, last)
	now = time.Unix(int64(last)-1, 0)
	checkMissing(1, last) // Clock rollback cannot make evidence fresh.
	if err := m.refresh(t.Context(), nil); err == nil {
		t.Fatal("nil reader accepted")
	}
	checkMissing(0, last)
}

func TestBuildRecoveryMetricsScrapeAndCancellationIsolation(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := NewBuildRecoveryMetrics(reg)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, done := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	reader := recoveryReadFunc(func(ctx context.Context) (state.BuildRecoverySnapshot, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-ctx.Done()
		return state.BuildRecoverySnapshot{}, nil // Late nil result must be rejected.
	})
	go func() { m.Run(ctx, reader, nil); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("poller did not start")
	}
	for range 25 {
		if got := queueMetrics(t, reg); len(got) != 2 {
			t.Fatalf("scrape queried or unblocked reader: %v", got)
		}
	}
	second := make(chan struct{})
	go func() { m.Run(ctx, reader, nil); close(second) }()
	cancel()
	for _, ch := range []chan struct{}{done, second} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("runner did not cancel")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("readers=%d", calls.Load())
	}
	if got := queueMetrics(t, reg); len(got) != 2 || got["apid_build_recovery_collection_success"] != 0 {
		t.Fatalf("late result accepted: %v", got)
	}
}
