package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/builderd"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type workerProcessFunc func(context.Context) (builderd.BuildResult, error)

func (f workerProcessFunc) ProcessNext(ctx context.Context) (builderd.BuildResult, error) {
	return f(ctx)
}

func workerGauges(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, family := range families {
		if len(family.Metric) == 1 && family.Metric[0].Gauge != nil {
			out[family.GetName()] = family.Metric[0].Gauge.GetValue()
		}
	}
	if len(out) != 7 {
		t.Fatalf("missing worker gauges: %v", out)
	}
	return out
}

func TestWorkerMetricsProgressAndOutcomes(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newBuildWorkerMetrics(reg, 2*time.Second, 25*time.Minute)
	now := time.Unix(1700000000, 0)
	m.now = func() time.Time { return now }
	m.snapshot.started = now
	for _, tc := range []struct {
		err     error
		outcome string
	}{
		{nil, "completed"}, {state.ErrNotFound, "not_claimed"}, {builderd.ErrNoSlot, "no_slot"},
		{fmt.Errorf("builderd: load deployment: %w", state.ErrNotFound), "error"},
		{errors.New("database unavailable"), "error"}, {context.Canceled, "canceled"}, {builderd.ErrDraining, "draining"},
	} {
		now = now.Add(time.Second)
		m.begin()
		// An entire normal build can occupy one poll iteration. A scrape
		// copies its start and inflight state without completing or renewing it.
		now = now.Add(15 * time.Minute)
		got := workerGauges(t, reg)
		if got["builderd_build_worker_inflight"] != 1 || got["builderd_build_worker_last_attempt_timestamp_seconds"] != float64(now.Add(-15*time.Minute).Unix()) || got["builderd_build_worker_busy_allowance_seconds"] != 1500 {
			t.Fatalf("busy observation=%v", got)
		}
		before := testutil.ToFloat64(m.iterations.WithLabelValues(tc.outcome))
		m.finish(tc.err)
		got = workerGauges(t, reg)
		if got["builderd_build_worker_inflight"] != 0 || got["builderd_build_worker_last_completed_timestamp_seconds"] != float64(now.Unix()) || testutil.ToFloat64(m.iterations.WithLabelValues(tc.outcome)) != before+1 {
			t.Fatalf("completed observation=%v", got)
		}
	}
}

func TestWorkerMetricsBlockedCallAndShutdown(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newBuildWorkerMetrics(reg, 5*time.Millisecond, 25*time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, done := make(chan struct{}), make(chan struct{})
	processor := workerProcessFunc(func(ctx context.Context) (builderd.BuildResult, error) {
		close(entered)
		<-ctx.Done()
		return builderd.BuildResult{}, ctx.Err()
	})
	go func() { workerLoopWithMetrics(ctx, processor, 5*time.Millisecond, discardLog(), m); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("poller never started")
	}
	for range 20 {
		if got := workerGauges(t, reg); got["builderd_build_worker_inflight"] != 1 || got["builderd_build_worker_last_completed_timestamp_seconds"] != 0 {
			t.Fatalf("blocked call reported complete: %v", got)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("poller failed to stop")
	}
	if got := workerGauges(t, reg); got["builderd_build_worker_enabled"] != 0 || got["builderd_build_worker_inflight"] != 0 {
		t.Fatalf("normal shutdown is still expected to poll: %v", got)
	}
}

func TestWorkerMetricsUnexpectedExitRemainsExpected(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		enabled float64
	}{{"unexpected cancel", context.Canceled, 1}, {"intentional drain", builderd.ErrDraining, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewPedanticRegistry()
			m := newBuildWorkerMetrics(reg, time.Millisecond, time.Minute)
			processor := workerProcessFunc(func(context.Context) (builderd.BuildResult, error) { return builderd.BuildResult{}, tc.err })
			workerLoopWithMetrics(t.Context(), processor, time.Millisecond, discardLog(), m)
			got := workerGauges(t, reg)
			if got["builderd_build_worker_enabled"] != tc.enabled || got["builderd_build_worker_last_completed_timestamp_seconds"] == 0 {
				t.Fatalf("exit state=%v", got)
			}
		})
	}
}
