// adr: 194 — attribute an admission to the signal that drove it.
package targets

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

// winningSignal scrapes the per-app winning-signal counter through the same
// Prometheus text endpoint schedd serves at /metrics.
func winningSignal(t *testing.T, ops *wire.OpsMetrics, app, metric string) float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	ops.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", rec.Code)
	}
	prefix := "schedd_scale_up_winning_signal_total{app=\"" + app + "\",metric=\"" + metric + "\"}"
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		return v
	}
	return 0
}

// TestWinningSignal_AttributesTheBindingTarget is the observability ADR-194
// promised and did not deliver: Decision.Winner was carried out of the
// arbiter and read by nothing, so an operator could see THAT an app scaled
// but not WHICH of its declared targets caused it.
//
// The app declares two targets. Queue depth demands 5 instances, in-flight
// demands 3, so the admission must be attributed to queue_depth and not to
// the signal that merely also happened to be hot.
func TestWinningSignal_AttributesTheBindingTarget(t *testing.T) {
	ops := wire.NewOpsMetrics("schedd")
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 20,
		ScalingPolicy: multiPolicy(0,
			state.ScalingTarget{Metric: api.ScalingMetricConcurrentRequests, Value: 4},
			state.ScalingTarget{Metric: api.ScalingMetricQueueDepth, Value: 5},
		),
	}}}
	tr := New(store, &fakeInstats{byApp: map[string]int64{"app1": 5}},
		&burstFakeEngine{fakeEngine: &fakeEngine{}},
		&fakeLedger{conc: map[string]int{"app1": 2}}, Options{
			Metrics:          ops,
			QueueStatsReader: &fakeQueueStats{byApp: map[string]state.QueueStats{"app1": {Depth: 24}}},
		})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := winningSignal(t, ops, "app1", api.ScalingMetricQueueDepth); got != 1 {
		t.Errorf("queue_depth attribution = %v, want 1", got)
	}
	if got := winningSignal(t, ops, "app1", api.ScalingMetricConcurrentRequests); got != 0 {
		t.Errorf("concurrent_requests attribution = %v, want 0: only the BINDING "+
			"target is attributed, not every hot one", got)
	}
}

// TestWinningSignal_NotEmittedWithoutAnAdmission keeps the counter's sum
// equal to _scale_up_decisions_total{outcome="admit"}. A no-signal tick has
// no winner, and emitting one would break that identity.
func TestWinningSignal_NotEmittedWithoutAnAdmission(t *testing.T) {
	ops := wire.NewOpsMetrics("schedd")
	store := &fakeStore{apps: []state.App{{
		ID:             "app1",
		MaxConcurrency: 20,
		ScalingPolicy:  multiPolicy(0, state.ScalingTarget{Metric: api.ScalingMetricConcurrentRequests, Value: 100}),
	}}}
	// One in-flight request against a target of 100: nowhere near hot.
	tr := New(store, &fakeInstats{byApp: map[string]int64{"app1": 1}}, &fakeEngine{},
		&fakeLedger{conc: map[string]int{"app1": 2}}, Options{Metrics: ops})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, m := range api.ScalingMetrics() {
		if got := winningSignal(t, ops, "app1", m); got != 0 {
			t.Errorf("%s attributed %v on a no_signal tick, want 0", m, got)
		}
	}
}
