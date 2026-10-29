package rolloutevidence

import (
	"context"
	"strings"
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

// adr: 121 — out-of-memory kills in the window degrade the status, ahead of
// regressions and stray server errors, and carry the remedy for the newest.
func TestStatusReportsOOMKills(t *testing.T) {
	kills := []api.DebugStatusOOMKill{
		{At: "2026-10-22T11:50:00Z", DeploymentID: testStable, PeakMB: 600, PlanMB: 512},
		{At: "2026-10-22T11:20:00Z", DeploymentID: testStable, PeakMB: 590, PlanMB: 512},
	}
	for _, tc := range []struct {
		name   string
		routes map[string]routeStats
		want   string
		head   string
	}{
		{
			name: "degraded", routes: map[string]routeStats{"/": {100, 1, 50}},
			want: api.DebugStatusDegraded, head: "3 out-of-memory kills in the last 1h; the newest peaked at 600 MB on a 512 MB plan.",
		},
		{
			name: "failing still wins", routes: map[string]routeStats{"/": {40, 4, 50}},
			want: api.DebugStatusFailing, head: "4 of 40 requests failed with a server error in the last 1h.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{live: []state.Deployment{{ID: testStable, AppID: testApp, TrafficPercent: 100}},
				routes: map[string]map[string]routeStats{testStable: tc.routes}}
			r := reader(store, nil, []api.DebugRegressionItem{{Route: "/a", LastDetectedAt: "1"}})
			var gotStart, gotEnd time.Time
			r.OOMKills = func(_ context.Context, appID string, start, end time.Time) ([]api.DebugStatusOOMKill, int, error) {
				if appID != testApp {
					t.Fatalf("OOM kills read for app %q", appID)
				}
				gotStart, gotEnd = start, end
				return kills, 3, nil
			}
			out, err := r.Status(context.Background(), state.App{ID: testApp}, time.Hour, 7*24*time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if out.Verdict != tc.want || out.Headline != tc.head {
				t.Fatalf("verdict=%q headline=%q", out.Verdict, out.Headline)
			}
			if !gotEnd.Equal(testNow) || !gotStart.Equal(testNow.Add(-time.Hour)) {
				t.Fatalf("OOM window = [%s, %s], want the status window", gotStart, gotEnd)
			}
			if out.OOMKillsTotal != 3 || len(out.OOMKills) != 2 || out.OOMKills[0].PeakMB != 600 {
				t.Fatalf("OOM kills = %d %+v", out.OOMKillsTotal, out.OOMKills)
			}
			if !strings.Contains(out.OOMFix, "512 MB plan") || !strings.Contains(out.OOMFix, "608 MB") {
				t.Fatalf("OOM fix = %q, want the plan and the peak plus 8 MB", out.OOMFix)
			}
		})
	}
}

func TestHumanWindow(t *testing.T) {
	for d, want := range map[time.Duration]string{time.Hour: "1h", 30 * time.Minute: "30m", 48 * time.Hour: "2d", 90 * time.Second: "1m30s"} {
		if got := FormatWindow(d); got != want {
			t.Fatalf("FormatWindow(%s) = %q, want %q", d, got, want)
		}
	}
}
