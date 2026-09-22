// adr: 195 — make an open scheduled window observable.
package floor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func scheduledFloorGauge(t *testing.T, ops *wire.OpsMetrics, app string) (float64, bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	ops.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	prefix := "schedd_scheduled_floor_instances{app=\"" + app + "\"}"
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		return v, true
	}
	return 0, false
}

// alwaysOpen keeps a window open continuously: a fire every minute with a
// two-minute duration means some window is always within its duration,
// whatever the wall clock says when the suite runs.
func alwaysOpen(min int) *state.ScalingPolicy {
	return &state.ScalingPolicy{
		Timezone:  "UTC",
		Schedules: []state.ScalingSchedule{{Cron: "* * * * *", DurationS: 120, MinInstances: min}},
	}
}

// TestScheduledFloorGauge_PublishesOpenWindow pins that an open ADR-195
// window is visible. Before this the only floor signal was
// meterd_floor_applied_total, which says a floor was applied and billed but
// not that a SCHEDULE raised it — so "is the 08:00 window actually open?"
// could only be answered by re-evaluating the cron by hand.
func TestScheduledFloorGauge_PublishesOpenWindow(t *testing.T) {
	ops := wire.NewOpsMetrics("schedd")
	app := floorApp("app1", api.PlanHobby, 0)
	app.ScalingPolicy = alwaysOpen(3)

	tr := New(&fakeStore{apps: []state.App{app}}, nil,
		&fakeLedger{conc: map[string]int{"app1": 0}, headroom: 47_600},
		&fakeEngine{}, Options{
			Metrics:      ops,
			PlanResolver: &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}},
			Auditor:      &fakeAuditor{},
		})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got, found := scheduledFloorGauge(t, ops, "app1")
	if !found || got != 3 {
		t.Fatalf("scheduled_floor_instances = (%v, found=%v), want 3", got, found)
	}
}

// TestScheduledFloorGauge_ZeroWhenNoWindowOpen is the half that matters
// operationally. A gauge that only ever rose would leave a closed window
// looking permanently open, and a scheduled floor is BILLED — "is it on when
// it should be" is a revenue question, not only an operational one.
//
// The app here has no schedules at all, which is the same observable state
// as every window being closed, and it also takes the floor<=0 disabled
// branch — the branch the emit deliberately sits above.
func TestScheduledFloorGauge_ZeroWhenNoWindowOpen(t *testing.T) {
	ops := wire.NewOpsMetrics("schedd")
	app := floorApp("app1", api.PlanHobby, 0)
	app.ScalingPolicy = &state.ScalingPolicy{}

	tr := New(&fakeStore{apps: []state.App{app}}, nil,
		&fakeLedger{conc: map[string]int{"app1": 0}, headroom: 47_600},
		&fakeEngine{}, Options{
			Metrics:      ops,
			PlanResolver: &fakePlanResolver{plans: map[string]api.Plan{"acct1": api.PlanHobby}},
			Auditor:      &fakeAuditor{},
		})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got, found := scheduledFloorGauge(t, ops, "app1")
	if !found {
		t.Fatal("no series for an app with no open window: the gauge must be published " +
			"as zero, not omitted, or a closed window is indistinguishable from a silent schedd")
	}
	if got != 0 {
		t.Errorf("scheduled_floor_instances = %v, want 0", got)
	}
}
