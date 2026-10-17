package operatorobs

import (
	"context"
	"errors"
	"maps"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

type buildQueueReadFunc func(context.Context) (state.BuildQueueSnapshot, error)

func (f buildQueueReadFunc) QueuedBuildsSnapshot(ctx context.Context) (state.BuildQueueSnapshot, error) {
	return f(ctx)
}

func queueMetrics(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, family := range families {
		if len(family.Metric) != 1 || len(family.Metric[0].Label) != 0 || family.Metric[0].Gauge == nil {
			t.Fatalf("expected one unlabelled gauge: %v", family)
		}
		out[family.GetName()] = family.Metric[0].Gauge.GetValue()
	}
	return out
}

func TestBuildQueueMetricsObservationLifecycle(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := NewBuildQueueMetrics(reg)
	now := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return now }
	lastSuccess, oldestSuccess := 0.0, 0.0
	check := func(t *testing.T, success float64, depth *float64) {
		t.Helper()
		want := map[string]float64{
			"apid_build_queue_collection_success":                        success,
			"apid_build_queue_collection_last_success_timestamp_seconds": lastSuccess,
		}
		if depth != nil {
			want["faas_build_queue_depth"] = *depth
			want["faas_build_queue_oldest_enqueued_timestamp_seconds"] = oldestSuccess
		}
		if got := queueMetrics(t, reg); !maps.Equal(got, want) {
			t.Fatalf("metrics = %v, want %v", got, want)
		}
	}
	check(t, 0, nil) // Startup is unknown, not an empty queue.
	for _, tc := range []struct {
		name  string
		count int
		err   error
	}{
		{"initial failure", 0, errors.New("unavailable")},
		{"empty queue", 0, nil},
		{"backlog", 73, nil},
		{"failure after backlog", 0, errors.New("unavailable")},
		{"invalid result", -1, nil},
		{"recovery", 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now = now.Add(30 * time.Second)
			started := now
			err := m.refresh(t.Context(), buildQueueReadFunc(func(ctx context.Context) (state.BuildQueueSnapshot, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > api.ObsBuildQueueCollectionBudget {
					t.Fatal("queue read lacks the collection deadline")
				}
				now = now.Add(2 * time.Second)
				snapshot := state.BuildQueueSnapshot{Queued: int64(tc.count)}
				if tc.count > 0 {
					snapshot.OldestEnqueuedAt = started.Add(-10 * time.Minute)
				}
				return snapshot, tc.err
			}))
			failed := tc.err != nil || tc.count < 0
			if (err != nil) != failed {
				t.Fatalf("refresh error = %v, failed = %v", err, failed)
			}
			if failed {
				check(t, 0, nil)
			} else {
				lastSuccess = float64(started.Unix()) // Read start, not completion.
				oldestSuccess = 0
				if tc.count > 0 {
					oldestSuccess = float64(started.Add(-10 * time.Minute).Unix())
				}
				depth := float64(tc.count)
				check(t, 1, &depth)
			}
		})
	}
	observed := m.observed
	zero := 0.0
	now = observed.Add(api.ObsBuildQueueFreshnessMaxAge)
	check(t, 1, &zero)
	now = now.Add(time.Nanosecond)
	check(t, 1, nil) // A stopped poller cannot keep exporting a healthy zero.
	now = observed.Add(-time.Second)
	check(t, 1, nil) // A backward clock step cannot renew future evidence.
}

func TestBuildQueueMetricsRunCancellationAndScrapeIsolation(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewBuildQueueMetrics(reg)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	entered := make(chan struct{})
	var calls atomic.Int32
	store := buildQueueReadFunc(func(ctx context.Context) (state.BuildQueueSnapshot, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-ctx.Done()
		return state.BuildQueueSnapshot{Queued: 99, OldestEnqueuedAt: time.Now().Add(-time.Hour)}, nil // Even a misbehaving late success must be rejected.
	})
	done := make(chan struct{}, 2)
	for range 2 {
		go func() { m.Run(ctx, store, nil); done <- struct{}{} }()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("poller did not perform an immediate read")
	}
	// Repeated scrapes must neither wait for SQL nor start another read.
	for range 10 {
		if got := queueMetrics(t, reg); got["apid_build_queue_collection_success"] != 0 {
			t.Fatal("unfinished read was presented as successful")
		}
	}
	for range 2 {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("poller did not stop after cancellation")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("two runners or scrapes performed %d reads, want 1", calls.Load())
	}
	got := queueMetrics(t, reg)
	if _, exists := got["faas_build_queue_depth"]; exists || got["apid_build_queue_collection_success"] != 0 {
		t.Fatalf("late result exported: %v", got)
	}
}

func TestBuildQueueMetricsSequentialTicksAndRecovery(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewBuildQueueMetrics(reg)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ticks := make(chan time.Time, 2)
	entered := make(chan int32, 3)
	release := make(chan struct{})
	var calls atomic.Int32
	store := buildQueueReadFunc(func(ctx context.Context) (state.BuildQueueSnapshot, error) {
		n := calls.Add(1)
		entered <- n
		if n == 1 {
			select {
			case <-release:
				return state.BuildQueueSnapshot{}, errors.New("unavailable")
			case <-ctx.Done():
				return state.BuildQueueSnapshot{}, ctx.Err()
			}
		}
		return state.BuildQueueSnapshot{Queued: 42, OldestEnqueuedAt: time.Now().Add(-time.Minute)}, nil
	})
	done := make(chan struct{})
	go func() { defer close(done); m.run(ctx, store, nil, ticks) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("initial read missing")
	}
	ticks <- time.Now()
	ticks <- time.Now()
	if calls.Load() != 1 {
		t.Fatal("ticks overlapped the unfinished read")
	}
	close(release)
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("queued tick was not processed")
		}
	}
	deadline := time.Now().Add(time.Second)
	for {
		got := queueMetrics(t, reg)
		if got["apid_build_queue_collection_success"] == 1 && got["faas_build_queue_depth"] == 42 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("collector did not recover: %v", got)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("poller did not stop")
	}
	if calls.Load() != 3 {
		t.Fatalf("read count = %d, want immediate + two ticks", calls.Load())
	}
}

func TestBuildQueueMetricsSnapshotConsistency(t *testing.T) {
	at := time.Unix(1_700_000_000, 0).UTC()
	for _, tc := range []struct {
		name     string
		snapshot state.BuildQueueSnapshot
		advance  time.Duration
		valid    bool
	}{
		{"empty", state.BuildQueueSnapshot{}, 0, true},
		{"old queued build", state.BuildQueueSnapshot{Queued: 1, OldestEnqueuedAt: at.Add(-time.Hour)}, 0, true},
		{"enqueue during read", state.BuildQueueSnapshot{Queued: 1, OldestEnqueuedAt: at.Add(time.Second)}, 2 * time.Second, true},
		{"empty with oldest", state.BuildQueueSnapshot{OldestEnqueuedAt: at}, 0, false},
		{"queued with no oldest", state.BuildQueueSnapshot{Queued: 1}, 0, false},
		{"future oldest", state.BuildQueueSnapshot{Queued: 1, OldestEnqueuedAt: at.Add(time.Hour)}, 0, false},
		{"pre-epoch oldest", state.BuildQueueSnapshot{Queued: 1, OldestEnqueuedAt: time.Unix(-1, 0)}, 0, false},
		{"negative count", state.BuildQueueSnapshot{Queued: -1}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewPedanticRegistry()
			m := NewBuildQueueMetrics(reg)
			now := at
			m.now = func() time.Time { return now }
			err := m.refresh(t.Context(), buildQueueReadFunc(func(context.Context) (state.BuildQueueSnapshot, error) {
				now = now.Add(tc.advance)
				return tc.snapshot, nil
			}))
			if (err == nil) != tc.valid {
				t.Fatalf("refresh = %v, valid = %v", err, tc.valid)
			}
			got := queueMetrics(t, reg)
			depth, hasDepth := got["faas_build_queue_depth"]
			oldest, hasOldest := got["faas_build_queue_oldest_enqueued_timestamp_seconds"]
			if hasDepth != tc.valid || hasOldest != tc.valid {
				t.Fatalf("partial or invalid snapshot: %v", got)
			}
			if tc.valid {
				wantOldest := 0.0
				if tc.snapshot.Queued > 0 {
					wantOldest = float64(tc.snapshot.OldestEnqueuedAt.Unix())
				}
				if depth != float64(tc.snapshot.Queued) || oldest != wantOldest {
					t.Fatalf("snapshot: %v", got)
				}
			}
		})
	}
}

func TestBuildQueueMetricsMissingSnapshotReader(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewBuildQueueMetrics(reg)
	if err := m.refresh(t.Context(), nil); err == nil {
		t.Fatal("missing reader reported success")
	}
	got := queueMetrics(t, reg)
	if len(got) != 2 || got["apid_build_queue_collection_success"] != 0 {
		t.Fatalf("missing reader: %v", got)
	}
}
