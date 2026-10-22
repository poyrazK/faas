package rolloutevidence

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 127 — debug status picks the worst condition and says why in one line.
func TestStatusVerdicts(t *testing.T) {
	stable := state.Deployment{ID: testStable, AppID: testApp, TrafficPercent: 100, RolloutState: "complete", CreatedAt: testNow.Add(-time.Hour)}
	abort := &api.RolloutDecision{Kind: api.RolloutDecisionAbort, Rule: api.RolloutRuleAbort5xx, TrafficPercent: 10, Summary: "5xx regression"}
	for _, tc := range []struct {
		name        string
		routes      map[string]routeStats
		deployments func(t *testing.T) []state.Deployment
		regressions []api.DebugRegressionItem
		want        string
		head        string
		failures    int
	}{
		{
			name: "failing", routes: map[string]routeStats{"/checkout": {40, 4, 50}},
			want: api.DebugStatusFailing, head: "4 of 40 requests failed with a server error in the last 1h.", failures: 2,
		},
		{
			name: "rolled back inside the window", routes: map[string]routeStats{"/checkout": {40, 0, 50}},
			deployments: func(t *testing.T) []state.Deployment {
				return []state.Deployment{{ID: testCandidate, AppID: testApp, CanaryTotalSteps: 4, RolloutState: "aborted",
					RolloutAbortedAt: at(-10 * time.Minute), CanaryStepStartedAt: at(-15 * time.Minute), RolloutDecision: encode(t, abort)}}
			},
			want: api.DebugStatusRollback, head: "Rolled back at 10% traffic: 5xx regression.",
		},
		{
			name: "rollback before the window is history", routes: map[string]routeStats{"/checkout": {40, 0, 50}},
			deployments: func(t *testing.T) []state.Deployment {
				return []state.Deployment{{ID: testCandidate, AppID: testApp, CanaryTotalSteps: 4, RolloutState: "aborted",
					RolloutAbortedAt: at(-3 * time.Hour), CanaryStepStartedAt: at(-4 * time.Hour), RolloutDecision: encode(t, abort)}}
			},
			want: api.DebugStatusHealthy, head: "40 requests in the last 1h, no server errors, p95 50 ms.",
		},
		{
			name: "degraded by a regression", routes: map[string]routeStats{"/checkout": {40, 0, 50}},
			regressions: []api.DebugRegressionItem{{Route: "/a", LastDetectedAt: "1"}, {Route: "/b", LastDetectedAt: "2"}},
			want:        api.DebugStatusDegraded, head: "2 regressions open in the last 1h.",
		},
		{
			name: "degraded by a few server errors", routes: map[string]routeStats{"/checkout": {100, 1, 50}},
			want: api.DebugStatusDegraded, head: "1 server error in the last 1h.", failures: 2,
		},
		{name: "idle", want: api.DebugStatusIdle, head: "No requests in the last 1h."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{
				live:    []state.Deployment{stable},
				routes:  map[string]map[string]routeStats{testStable: tc.routes},
				summary: sqlc.RequestTelemetryAnalyticsSummaryRow{P95Ms: 50},
				rows:    []sqlc.ListRequestTelemetryByAppRow{row("/checkout", 500, 10), row("/checkout", 200, 10), row("/checkout", 503, 10)},
			}
			if tc.deployments != nil {
				store.deployments = tc.deployments(t)
			}
			out, err := reader(store, nil, tc.regressions).Status(context.Background(), state.App{ID: testApp}, time.Hour, 7*24*time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if out.Verdict != tc.want || out.Headline != tc.head {
				t.Fatalf("verdict=%q headline=%q", out.Verdict, out.Headline)
			}
			if len(out.RecentFailures) != tc.failures || len(out.Deployments) != 1 || out.Deployments[0].ID != testStable {
				t.Fatalf("failures=%+v deployments=%+v", out.RecentFailures, out.Deployments)
			}
			if len(tc.regressions) > 0 && (out.RegressionsTotal != 2 || out.Regressions[0].Route != "/b") {
				t.Fatalf("regressions not newest first: %+v", out.Regressions)
			}
		})
	}
}

func TestStatusReportsReleaseAndTracing(t *testing.T) {
	stable := state.Deployment{ID: testStable, AppID: testApp, TrafficPercent: 90}
	candidate := state.Deployment{ID: testCandidate, AppID: testApp, TrafficPercent: 10, CanaryTotalSteps: 4, RolloutState: "rolling_out"}
	store := &fakeStore{
		deployments: []state.Deployment{candidate, stable}, live: []state.Deployment{stable, candidate},
		routes: map[string]map[string]routeStats{testStable: {"/": {90, 0, 20}}, testCandidate: {"/": {10, 0, 25}}},
	}
	app := state.App{ID: testApp, Manifest: state.AppManifest{Tracing: &api.TracingConfig{Enabled: true}}}
	out, err := reader(store, nil, nil).Status(context.Background(), app, time.Hour, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if out.Verdict != api.DebugStatusReleasing || out.Headline != "Rolling out at 10% traffic." || out.Rollout == nil || !out.TracingEnabled {
		t.Fatalf("status = %+v", out)
	}
	if out.Health.Requests != 100 || out.Deployments[0].ID != testStable || out.Deployments[1].TrafficPercent != 10 {
		t.Fatalf("health=%+v deployments=%+v", out.Health, out.Deployments)
	}
}

func TestHumanWindow(t *testing.T) {
	for d, want := range map[time.Duration]string{time.Hour: "1h", 30 * time.Minute: "30m", 48 * time.Hour: "2d", 90 * time.Second: "1m30s"} {
		if got := humanWindow(d); got != want {
			t.Fatalf("humanWindow(%s) = %q, want %q", d, got, want)
		}
	}
}
