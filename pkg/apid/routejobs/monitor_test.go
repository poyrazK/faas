package routejobs_test

// adr: 20261024154324

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routejobs"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeQueue serves due monitors RouteMonitorBatchSize at a time, oldest
// first, like ListDueRouteMonitors. An evaluated monitor stops being due;
// held monitors stay due but evaluate as skipped (another replica holds
// them); failing monitors error and are deferred.
type fakeQueue struct {
	mu        sync.Mutex
	due       []state.RouteMonitorTarget
	held      map[string]bool
	failing   map[string]bool
	evaluated []string
	deferred  []string
	inFlight  int
	maxFlight int
}

func (q *fakeQueue) ListDueRouteMonitors(context.Context) ([]state.RouteMonitorTarget, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.due[:min(len(q.due), api.RouteMonitorBatchSize)]), nil
}

func (q *fakeQueue) EvaluateRouteMonitor(_ context.Context, _, appID string) (bool, error) {
	q.mu.Lock()
	q.inFlight++
	q.maxFlight = max(q.maxFlight, q.inFlight)
	q.mu.Unlock()
	time.Sleep(time.Millisecond)
	q.mu.Lock()
	defer q.mu.Unlock()
	q.inFlight--
	switch {
	case q.failing[appID]:
		return false, errors.New("boom")
	case q.held[appID]:
		return false, nil
	}
	q.evaluated = append(q.evaluated, appID)
	q.due = slices.DeleteFunc(q.due, func(t state.RouteMonitorTarget) bool { return t.AppID == appID })
	return true, nil
}

func (q *fakeQueue) DeferRouteMonitor(_ context.Context, t state.RouteMonitorTarget) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.deferred = append(q.deferred, t.AppID)
	q.due = slices.DeleteFunc(q.due, func(d state.RouteMonitorTarget) bool { return d.AppID == t.AppID })
	return nil
}

func dueMonitors(n int, oldest time.Time) []state.RouteMonitorTarget {
	out := make([]state.RouteMonitorTarget, n)
	for i := range out {
		out[i] = state.RouteMonitorTarget{AccountID: "acct", AppID: "app-" + strconv.Itoa(i), DueAt: oldest.Add(time.Duration(i) * time.Second)}
	}
	return out
}

func TestMonitorDrainEvaluatesBeyondOneBatch(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	total := 5*api.RouteMonitorBatchSize + 3
	q := &fakeQueue{due: dueMonitors(total, now.Add(-4*time.Minute))}
	m := routejobs.NewMetrics(prometheus.NewRegistry(), "apid")
	var rolledBack sync.Map
	n, err := routejobs.MonitorDrain{
		Queue: q, Metrics: m, Now: func() time.Time { return now },
		AfterEvaluate: func(_ context.Context, t state.RouteMonitorTarget) { rolledBack.Store(t.AppID, true) },
	}.Run(t.Context())
	if err != nil || n != total || len(q.evaluated) != total {
		t.Fatalf("drain completed %d (evaluated %d), err %v; want %d", n, len(q.evaluated), err, total)
	}
	if q.maxFlight < 2 || q.maxFlight > api.RouteMonitorWorkerConcurrency {
		t.Fatalf("peak concurrent evaluations = %d, want 2..%d", q.maxFlight, api.RouteMonitorWorkerConcurrency)
	}
	if _, ok := rolledBack.Load("app-0"); !ok {
		t.Fatal("rollback hook did not run after an evaluation")
	}
	if got := sample(t, m.Registry(), "apid_route_monitor_lag_seconds", ""); got != (4 * time.Minute).Seconds() {
		t.Fatalf("lag = %vs, want 240s", got)
	}
}

func TestMonitorDrainStopsWhenOnlyTakenMonitorsRemain(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	q := &fakeQueue{
		due:     dueMonitors(3, now),
		held:    map[string]bool{"app-0": true},
		failing: map[string]bool{"app-1": true},
	}
	reg := prometheus.NewRegistry()
	n, err := routejobs.MonitorDrain{Queue: q, Metrics: routejobs.NewMetrics(reg, "apid"), Now: func() time.Time { return now }}.Run(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("drain completed %d, err %v; want 1", n, err)
	}
	if !slices.Equal(q.deferred, []string{"app-1"}) {
		t.Fatalf("deferred = %v, want the failing monitor", q.deferred)
	}
	want := map[string]float64{routejobs.MonitorEvaluated: 1, routejobs.MonitorSkipped: 1, routejobs.MonitorFailed: 1}
	for outcome, v := range want {
		if got := sample(t, reg, "apid_route_monitor_evaluations_total", outcome); got != v {
			t.Fatalf("%s evaluations = %v, want %v", outcome, got, v)
		}
	}
}

// sample reads one gathered series, selected by its outcome label when set.
func sample(t *testing.T, reg *prometheus.Registry, name, outcome string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, metric := range f.GetMetric() {
			if outcome != "" && (len(metric.GetLabel()) == 0 || metric.GetLabel()[0].GetValue() != outcome) {
				continue
			}
			return metric.GetGauge().GetValue() + metric.GetCounter().GetValue()
		}
	}
	t.Fatalf("series %s{outcome=%q} not gathered", name, outcome)
	return 0
}
