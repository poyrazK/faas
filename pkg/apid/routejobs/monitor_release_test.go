package routejobs_test

// adr: 20261028110044

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/apid/routejobs"
	"github.com/onebox-faas/faas/pkg/state"
)

// A monitor whose capability is off for its account is deferred without
// evaluating: no incident, no webhook and no automatic rollback.
func TestMonitorDrainDefersUnreleasedMonitors(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	q := &fakeQueue{due: dueMonitors(4, now.Add(-time.Minute))}
	m := routejobs.NewMetrics(prometheus.NewRegistry(), "apid")
	var rolledBack sync.Map
	n, err := routejobs.MonitorDrain{
		Queue: q, Metrics: m, Now: func() time.Time { return now },
		AfterEvaluate: func(_ context.Context, target state.RouteMonitorTarget) { rolledBack.Store(target.AppID, true) },
		Released: func(_ context.Context, target state.RouteMonitorTarget) bool {
			return target.AppID != "app-1" && target.AppID != "app-3"
		},
	}.Run(t.Context())
	if err != nil || n != 2 {
		t.Fatalf("drain completed %d, err %v; want the 2 released monitors", n, err)
	}
	slices.Sort(q.evaluated)
	slices.Sort(q.deferred)
	if !slices.Equal(q.evaluated, []string{"app-0", "app-2"}) || !slices.Equal(q.deferred, []string{"app-1", "app-3"}) {
		t.Fatalf("evaluated %v, deferred %v; want app-0/app-2 evaluated and app-1/app-3 deferred", q.evaluated, q.deferred)
	}
	for _, app := range []string{"app-1", "app-3"} {
		if _, ok := rolledBack.Load(app); ok {
			t.Fatalf("rollback hook ran for unreleased %s", app)
		}
	}
	if got := sample(t, m.Registry(), "apid_route_monitor_evaluations_total", "unreleased"); got != 2 {
		t.Fatalf("unreleased evaluations = %v, want 2", got)
	}
}
