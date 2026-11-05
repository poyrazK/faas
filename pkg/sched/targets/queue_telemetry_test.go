// adr: 20261018101500 — amendment 2026-10-27: bounded queue reads and gauges every worker.
package targets

import (
	"context"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// boundedQueueBindings serves only the bounded read and records each limit.
type boundedQueueBindings struct {
	fakeQueueBindings
	mu     sync.Mutex
	limits map[string][]int
	full   int
}

func (q *boundedQueueBindings) QueueBindingTelemetry(ctx context.Context, appID, bindingID string, limit int) (state.QueueStats, error) {
	q.mu.Lock()
	if q.limits == nil {
		q.limits = map[string][]int{}
	}
	q.limits[appID] = append(q.limits[appID], limit)
	q.mu.Unlock()
	stats, err := q.fakeQueueBindings.QueueStateForBinding(ctx, appID, bindingID)
	stats.Depth = min(stats.Depth, limit)
	return stats, err
}

func (q *boundedQueueBindings) QueueStateForBinding(context.Context, string, string) (state.QueueStats, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.full++
	return state.QueueStats{}, nil
}

func queueDepthGauge(t *testing.T, metrics *wire.OpsMetrics, app string) (float64, bool) {
	t.Helper()
	families, err := metrics.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "schedd_queue_depth" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "app" && label.GetValue() == app {
					return metric.GetGauge().GetValue(), true
				}
			}
		}
	}
	return 0, false
}

func TestTrigger_QueueReadsAreBoundedAndGaugesThrottled(t *testing.T) {
	depthPolicy := &state.ScalingPolicy{Targets: []state.ScalingTarget{{Metric: api.ScalingMetricQueueDepth, Value: 10}}}
	apps := []state.App{
		{ID: "scaled-worker", WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 5, ScalingPolicy: depthPolicy},
		{ID: "plain-worker", WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 5},
		{ID: "http-consumer", MaxConcurrency: 5, ScalingPolicy: depthPolicy},
	}
	bindings := &boundedQueueBindings{fakeQueueBindings: fakeQueueBindings{
		byApp:   map[string][]state.QueueBinding{},
		byQueue: map[string]state.QueueStats{},
	}}
	for _, app := range apps {
		bindings.byApp[app.ID] = []state.QueueBinding{{ID: app.ID + "-jobs", AppID: app.ID, QueueName: app.ID, Enabled: true, MaxConcurrency: 5}}
		bindings.byQueue[app.ID] = state.QueueStats{Depth: 250_000}
	}
	engine := &scopedWorkerPoolFake{workerPoolFakeEngine: &workerPoolFakeEngine{fakeEngine: &fakeEngine{}}}
	metrics := wire.NewOpsMetrics("schedd")
	tr := New(&fakeStore{apps: apps}, nil, engine, &fakeLedger{conc: map[string]int{}}, Options{
		Metrics: metrics, QueueBindingStatsReader: bindings,
	})
	for range 3 {
		if err := tr.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		tr.waitWorkerPoolRuns()
	}
	bindings.mu.Lock()
	defer bindings.mu.Unlock()
	if bindings.full != 0 {
		t.Fatalf("full binding reads = %d, want only bounded reads", bindings.full)
	}
	// Workers read only for the gauges, once per interval, whatever they
	// scale on.
	for _, app := range []string{"scaled-worker", "plain-worker"} {
		if got := bindings.limits[app]; len(got) != 1 || got[0] != queueTelemetryLimit {
			t.Fatalf("%s reads = %v, want one gauge read", app, got)
		}
		if depth, ok := queueDepthGauge(t, metrics, app); !ok || depth != queueTelemetryLimit {
			t.Fatalf("%s gauge = %v, %v", app, depth, ok)
		}
	}
	// A queue-scaled HTTP consumer reads every tick, bounded to what the
	// decision needs except on the tick that refreshes its gauges.
	want := []int{queueTelemetryLimit, queueScalingLimit(10, 5), queueScalingLimit(10, 5)}
	if got := bindings.limits["http-consumer"]; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("http consumer reads = %v, want %v", got, want)
	}
}

func TestQueueScalingLimit(t *testing.T) {
	for _, tc := range []struct {
		target float64
		max    int
		want   int
	}{
		{target: 10, max: 5, want: 61},
		{target: 0, max: 5, want: 1},
		{target: 10, max: 0, want: queueTelemetryLimit},
		{target: 1e6, max: 20, want: queueTelemetryLimit},
	} {
		if got := queueScalingLimit(tc.target, tc.max); got != tc.want {
			t.Errorf("queueScalingLimit(%v, %d) = %d, want %d", tc.target, tc.max, got, tc.want)
		}
	}
}
