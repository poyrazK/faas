package rolloutevidence

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const (
	testApp       = "00000000-0000-4000-8000-0000000000a1"
	testCandidate = "00000000-0000-4000-8000-0000000000c1"
	testStable    = "00000000-0000-4000-8000-0000000000b1"
	testOther     = "00000000-0000-4000-8000-0000000000d1"
)

var testNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

type routeStats struct {
	requests, errors int64
	p95              int32
}

type fakeStore struct {
	deployments []state.Deployment // newest first
	live        []state.Deployment
	routes      map[string]map[string]routeStats // deployment → route → stats
	rows        []sqlc.ListRequestTelemetryByAppRow
	summary     sqlc.RequestTelemetryAnalyticsSummaryRow
	listed      []sqlc.ListRequestTelemetryByAppParams
	windows     []pgtype.Timestamptz
}

func (f *fakeStore) DeploymentByID(_ context.Context, id string) (state.Deployment, error) {
	for _, dep := range f.deployments {
		if dep.ID == id {
			return dep, nil
		}
	}
	return state.Deployment{}, state.ErrNotFound
}

func (f *fakeStore) ListDeploymentsForApp(_ context.Context, _ string, limit, _ int) ([]state.Deployment, error) {
	return f.deployments[:min(limit, len(f.deployments))], nil
}

func (f *fakeStore) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	return f.live, nil
}

func (f *fakeStore) RequestTelemetryErrorsByRoute(_ context.Context, arg sqlc.RequestTelemetryErrorsByRouteParams) ([]sqlc.RequestTelemetryErrorsByRouteRow, error) {
	f.windows = append(f.windows, arg.ReceivedAt)
	var out []sqlc.RequestTelemetryErrorsByRouteRow
	for route, stats := range f.routes[uuid.UUID(arg.DeploymentID.Bytes).String()] {
		out = append(out, sqlc.RequestTelemetryErrorsByRouteRow{Route: route, Requests: stats.requests, ErrorRequests: stats.errors})
	}
	return out, nil
}

func (f *fakeStore) RequestTelemetryBaselineP95ByRoute(_ context.Context, arg sqlc.RequestTelemetryBaselineP95ByRouteParams) ([]sqlc.RequestTelemetryBaselineP95ByRouteRow, error) {
	var out []sqlc.RequestTelemetryBaselineP95ByRouteRow
	for route, stats := range f.routes[uuid.UUID(arg.DeploymentID.Bytes).String()] {
		out = append(out, sqlc.RequestTelemetryBaselineP95ByRouteRow{Route: route, P95Ms: stats.p95, N: stats.requests})
	}
	return out, nil
}

func (f *fakeStore) RequestTelemetryAnalyticsSummary(context.Context, sqlc.RequestTelemetryAnalyticsSummaryParams) (sqlc.RequestTelemetryAnalyticsSummaryRow, error) {
	return f.summary, nil
}

func (f *fakeStore) ListRequestTelemetryByApp(_ context.Context, arg sqlc.ListRequestTelemetryByAppParams) ([]sqlc.ListRequestTelemetryByAppRow, error) {
	f.listed = append(f.listed, arg)
	var out []sqlc.ListRequestTelemetryByAppRow
	for _, row := range f.rows {
		if arg.Route != "" && row.Route != arg.Route || row.LatencyMs < arg.MinLatencyMs {
			continue
		}
		out = append(out, row)
	}
	return out[:min(int(arg.Limit), len(out))], nil
}

type fakeHolds map[string]*api.RolloutDecision

func (f fakeHolds) LatestRolloutHold(_ context.Context, id string) (json.RawMessage, error) {
	if f[id] == nil {
		return nil, nil
	}
	return json.Marshal(f[id])
}

func at(d time.Duration) *time.Time {
	t := testNow.Add(d)
	return &t
}

func encode(t *testing.T, d *api.RolloutDecision) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func row(route string, status, latency int32) sqlc.ListRequestTelemetryByAppRow {
	return sqlc.ListRequestTelemetryByAppRow{Route: route, Method: "GET", Status: status, LatencyMs: latency}
}

func reader(store *fakeStore, holds fakeHolds, regressions []api.DebugRegressionItem) Reader {
	return Reader{
		Store: store, Holds: holds, Now: func() time.Time { return testNow },
		Regressions: func(context.Context, string, time.Duration) ([]api.DebugRegressionItem, error) {
			return regressions, nil
		},
	}
}

// adr: 127 — an aborted canary is explained with the routes and requests
// behind its ADR-1447 decision, over the decision's own window.
func TestExplainAbortedWithDecision(t *testing.T) {
	abort := &api.RolloutDecision{
		Kind: api.RolloutDecisionAbort, Rule: api.RolloutRuleAbort5xx, TrafficPercent: 10,
		Summary:     "5xx regression (candidate 95.2%, stable 0.0%; 20/21 vs 0/21 requests)",
		WindowStart: testNow.Add(-20 * time.Minute).Format(time.RFC3339Nano), WindowEnd: testNow.Add(-15 * time.Minute).Format(time.RFC3339Nano),
		StableDeploymentID: testStable,
	}
	store := &fakeStore{
		deployments: []state.Deployment{
			{ID: testCandidate, AppID: testApp, CanaryTotalSteps: 4, RolloutState: "aborted", RolloutAbortedAt: at(-15 * time.Minute), RolloutDecision: encode(t, abort)},
			{ID: testStable, AppID: testApp, RolloutState: "complete"},
		},
		routes: map[string]map[string]routeStats{
			testCandidate: {"/checkout": {21, 20, 90}, "/health": {40, 0, 5}, "/cart": {10, 1, 30}},
			testStable:    {"/checkout": {21, 0, 80}, "/health": {40, 0, 5}, "/cart": {10, 1, 30}},
		},
		rows: []sqlc.ListRequestTelemetryByAppRow{row("/checkout", 200, 10), row("/checkout", 500, 12), row("/checkout", 502, 14), row("/checkout", 500, 9), row("/checkout", 500, 9)},
	}
	regressions := []api.DebugRegressionItem{{DeploymentID: testCandidate, Route: "/checkout", Kind: "errors"}, {DeploymentID: testOther, Route: "/x"}}
	out, err := reader(store, nil, regressions).Explain(context.Background(), state.App{ID: testApp}, "", 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != api.DebugRolloutAborted || out.Decision == nil || out.Decision.Rule != api.RolloutRuleAbort5xx || out.StableDeploymentID != testStable {
		t.Fatalf("explanation = %+v", out)
	}
	if out.WindowStart != abort.WindowStart || out.WindowEnd != abort.WindowEnd {
		t.Fatalf("window = %s..%s, want the decision's", out.WindowStart, out.WindowEnd)
	}
	if len(out.Routes) != 3 || out.Routes[0].Route != "/checkout" || out.Routes[0].StableRequests != 21 || out.Routes[2].Route != "/health" {
		t.Fatalf("routes = %+v", out.Routes)
	}
	if len(out.Examples) != 3 || out.Examples[0].Status != 500 || out.Examples[1].Status != 502 {
		t.Fatalf("examples = %+v", out.Examples)
	}
	if len(out.Regressions) != 1 || out.Regressions[0].Route != "/checkout" {
		t.Fatalf("regressions = %+v", out.Regressions)
	}
	want := "Rolled back at 10% traffic: 5xx regression (candidate 95.2%, stable 0.0%; 20/21 vs 0/21 requests). " +
		"Worst route /checkout: 20 of 21 requests failed with a server error, vs 0 of 21 on the stable release."
	if out.Headline != want {
		t.Fatalf("headline = %q\nwant       %q", out.Headline, want)
	}
}

func TestExplainStates(t *testing.T) {
	hold := &api.RolloutDecision{Kind: api.RolloutDecisionHold, Rule: api.RolloutRuleHoldInsufficient, TrafficPercent: 5,
		Summary: "waiting for 20 requests (have 3)", WindowStart: testNow.Add(-4 * time.Minute).Format(time.RFC3339Nano), WindowEnd: testNow.Add(-time.Minute).Format(time.RFC3339Nano)}
	latency := &api.RolloutDecision{Kind: api.RolloutDecisionAbort, Rule: api.RolloutRuleAbortP95Latency, TrafficPercent: 25, Summary: "p95 regression"}
	for _, tc := range []struct {
		name        string
		dep         state.Deployment
		holds       fakeHolds
		routes      map[string]map[string]routeStats
		wantState   string
		wantHead    string
		wantMinLat  int32
		wantWindowE string
	}{
		{
			name:  "held",
			dep:   state.Deployment{ID: testCandidate, AppID: testApp, CanaryTotalSteps: 4, RolloutState: "rolling_out", TrafficPercent: 5, CanaryStepStartedAt: at(-5 * time.Minute)},
			holds: fakeHolds{testCandidate: hold}, wantState: api.DebugRolloutHeld,
			wantHead:    "Held at 5% traffic: waiting for 20 requests (have 3).",
			wantWindowE: testNow.Format(time.RFC3339Nano),
		},
		{
			name:      "latency abort ranks by p95 and picks slow examples",
			dep:       state.Deployment{ID: testCandidate, AppID: testApp, CanaryTotalSteps: 4, RolloutState: "aborted", RolloutAbortedAt: at(-time.Minute), CanaryStepStartedAt: at(-6 * time.Minute)},
			routes:    map[string]map[string]routeStats{testCandidate: {"/fast": {50, 2, 40}, "/search": {30, 0, 900}}, testStable: {"/fast": {50, 0, 35}, "/search": {30, 0, 200}}},
			wantState: api.DebugRolloutAborted, wantMinLat: 900,
			wantHead: "Rolled back at 25% traffic: p95 regression. Slowest route /search: p95 900 ms vs 200 ms on the stable release.",
		},
		{
			name:      "abort by another path keeps its reason",
			dep:       state.Deployment{ID: testCandidate, AppID: testApp, CanaryTotalSteps: 4, RolloutState: "aborted", RolloutAbortedReason: "route monitor: /checkout unhealthy", RolloutAbortedAt: at(-time.Minute)},
			wantState: api.DebugRolloutAborted,
			wantHead:  "Rolled back: route monitor: /checkout unhealthy.",
		},
		{
			name:      "rolling out",
			dep:       state.Deployment{ID: testCandidate, AppID: testApp, CanaryTotalSteps: 4, RolloutState: "rolling_out", TrafficPercent: 50},
			wantState: api.DebugRolloutRollingOut, wantHead: "Rolling out at 50% traffic.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wantState == api.DebugRolloutAborted && tc.dep.RolloutAbortedReason == "" {
				tc.dep.RolloutDecision = encode(t, latency)
			}
			stable := state.Deployment{ID: testStable, AppID: testApp, TrafficPercent: 100}
			store := &fakeStore{deployments: []state.Deployment{tc.dep, stable}, live: []state.Deployment{stable, tc.dep}, routes: tc.routes,
				rows: []sqlc.ListRequestTelemetryByAppRow{row("/search", 200, 950), row("/search", 200, 120)}}
			out, err := reader(store, tc.holds, nil).Explain(context.Background(), state.App{ID: testApp}, "", 7*24*time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if out.State != tc.wantState || out.Headline != tc.wantHead || out.StableDeploymentID != testStable {
				t.Fatalf("state=%q stable=%q headline=%q", out.State, out.StableDeploymentID, out.Headline)
			}
			if tc.wantWindowE != "" && out.WindowEnd != tc.wantWindowE {
				t.Fatalf("hold window end = %s, want now", out.WindowEnd)
			}
			if tc.wantMinLat > 0 {
				if len(store.listed) != 1 || store.listed[0].MinLatencyMs != tc.wantMinLat || len(out.Examples) != 1 || out.Examples[0].LatencyMS != 950 {
					t.Fatalf("latency examples: listed=%+v examples=%+v", store.listed, out.Examples)
				}
			}
		})
	}
}

func TestExplainSelection(t *testing.T) {
	plain := state.Deployment{ID: testOther, AppID: testApp}
	store := &fakeStore{deployments: []state.Deployment{plain}}
	out, err := reader(store, nil, nil).Explain(context.Background(), state.App{ID: testApp}, "", time.Hour)
	if err != nil || out.State != api.DebugRolloutNone || out.DeploymentID != "" || !strings.HasPrefix(out.Headline, "No canary rollout") {
		t.Fatalf("no rollout: %+v err=%v", out, err)
	}
	store.deployments = append(store.deployments, state.Deployment{ID: testCandidate, AppID: "00000000-0000-4000-8000-0000000000ff"})
	if _, err := reader(store, nil, nil).Explain(context.Background(), state.App{ID: testApp}, testCandidate, time.Hour); !errors.Is(err, ErrDeploymentNotFound) {
		t.Fatalf("other app's deployment: err=%v", err)
	}
	if _, err := reader(store, nil, nil).Explain(context.Background(), state.App{ID: testApp}, testStable, time.Hour); !errors.Is(err, ErrDeploymentNotFound) {
		t.Fatalf("unknown deployment: err=%v", err)
	}
}

// The evidence window never reaches before the plan's debugger retention.
func TestExplainClampsToRetention(t *testing.T) {
	dep := state.Deployment{ID: testCandidate, AppID: testApp, CanaryTotalSteps: 4, RolloutState: "rolling_out", CanaryStepStartedAt: at(-48 * time.Hour)}
	store := &fakeStore{deployments: []state.Deployment{dep}, routes: map[string]map[string]routeStats{testCandidate: {"/": {1, 0, 1}}}}
	out, err := reader(store, nil, nil).Explain(context.Background(), state.App{ID: testApp}, "", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !out.RetentionClamped || out.WindowStart != testNow.Add(-24*time.Hour).Format(time.RFC3339Nano) || store.windows[0].Time != testNow.Add(-24*time.Hour) {
		t.Fatalf("clamp: clamped=%v start=%s queried=%v", out.RetentionClamped, out.WindowStart, store.windows)
	}
}
