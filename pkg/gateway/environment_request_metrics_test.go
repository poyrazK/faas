package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// adr: 744 guards the separation of request metrics used by scoped baselines.
func TestEnvironmentRequestMetricsScopes(t *testing.T) {
	m := newEnvironmentRequestMetrics()
	app := App{ID: "orders", Plan: api.PlanPro, ProjectID: "shop"}
	production := withRequestEnvironment(t.Context(), app)
	production = context.WithValue(production, publicRoutingSnapshotKey{}, PublicRoutingSnapshot{
		AppID: app.ID, Scope: "production", SelectedDeploymentID: "production-release", SelectionReason: "weighted",
	})
	staging := app
	staging.PublicEnvironmentID = "staging-env"
	staging.PinnedDeploymentID = "staging-release"
	staging.PinnedDeploymentScope = "staging"
	stage := withRequestEnvironment(t.Context(), staging)
	m.observe(production, app.ID, "2xx", 10*time.Millisecond, false)
	m.observe(stage, app.ID, "5xx", 300*time.Millisecond, false)
	m.observe(stage, app.ID, "4xx", 50*time.Millisecond, true)

	direct := staging
	direct.PublicEnvironmentID = ""
	m.observe(withRequestEnvironment(t.Context(), direct), app.ID, "5xx", time.Second, false)
	legacy := context.WithValue(withRequestEnvironment(t.Context(), app), publicRoutingSnapshotKey{}, PublicRoutingSnapshot{
		AppID: app.ID, Scope: "production", RosterScope: api.DefaultEnvScope,
		SelectedDeploymentID: "legacy-release", SelectionReason: "weighted",
	})
	m.observe(legacy, app.ID, "2xx", 10*time.Millisecond, false)
	standalone := app
	standalone.ProjectID = ""
	namedRoster := context.WithValue(withRequestEnvironment(t.Context(), standalone), publicRoutingSnapshotKey{}, PublicRoutingSnapshot{
		AppID: app.ID, RosterScope: "production", SelectedDeploymentID: "production-release", SelectionReason: "weighted",
	})
	m.observe(namedRoster, app.ID, "2xx", 10*time.Millisecond, false)

	for _, tc := range []struct {
		scope, class string
		count        uint64
	}{
		{"staging", "5xx", 1}, {"staging", "2xx", 0}, {"production", "5xx", 0},
		{"production", "2xx", 2}, {api.DefaultEnvScope, "2xx", 1},
	} {
		var sample dto.Metric
		histogram := m.duration.WithLabelValues(app.ID, tc.scope, tc.class).(prometheus.Metric)
		if err := histogram.Write(&sample); err != nil || sample.GetHistogram().GetSampleCount() != tc.count {
			t.Errorf("%s %s: count=%d, want %d, err=%v", tc.scope, tc.class, sample.GetHistogram().GetSampleCount(), tc.count, err)
		}
	}
	if got := testutil.ToFloat64(m.refusals.WithLabelValues(app.ID, "staging")); got != 1 {
		t.Errorf("staging refusals=%g, want 1", got)
	}
	if got := testutil.ToFloat64(m.refusals.WithLabelValues(app.ID, "production")); got != 0 {
		t.Errorf("production refusals=%g, want 0", got)
	}
}
