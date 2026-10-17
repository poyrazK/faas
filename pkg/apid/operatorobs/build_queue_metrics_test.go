package operatorobs

import (
	"context"
	"errors"
	"maps"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

type buildQueueReadFunc func(context.Context) (int, error)

func (f buildQueueReadFunc) QueuedBuildsCount(ctx context.Context) (int, error) { return f(ctx) }

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
	lastSuccess := 0.0
	check := func(t *testing.T, success float64, depth *float64) {
		t.Helper()
		want := map[string]float64{
			"apid_build_queue_collection_success":                        success,
			"apid_build_queue_collection_last_success_timestamp_seconds": lastSuccess,
		}
		if depth != nil {
			want["faas_build_queue_depth"] = *depth
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
			err := m.refresh(t.Context(), buildQueueReadFunc(func(ctx context.Context) (int, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > api.ObsBuildQueueCollectionBudget {
					t.Fatal("queue read lacks the collection deadline")
				}
				now = now.Add(2 * time.Second)
				return tc.count, tc.err
			}))
			failed := tc.err != nil || tc.count < 0
			if (err != nil) != failed {
				t.Fatalf("refresh error = %v, failed = %v", err, failed)
			}
			if failed {
				check(t, 0, nil)
			} else {
				lastSuccess = float64(started.Unix()) // Read start, not completion.
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
	store := buildQueueReadFunc(func(ctx context.Context) (int, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-ctx.Done()
		return 99, nil // Even a misbehaving late success must be rejected.
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
	store := buildQueueReadFunc(func(ctx context.Context) (int, error) {
		n := calls.Add(1)
		entered <- n
		if n == 1 {
			select {
			case <-release:
				return 0, errors.New("unavailable")
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		return 42, nil
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
