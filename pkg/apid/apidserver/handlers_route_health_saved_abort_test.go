package apidserver_test

// adr: 458

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type savedAbortRouteHealthStore struct {
	MemStore *state.MemStore
	live     api.RouteHealthReport
	newest   []api.RouteHealthHistoryEntry
}

func (s *savedAbortRouteHealthStore) GetRouteHealthReport(context.Context, string, string, string) (api.RouteHealthReport, error) {
	return s.live, nil
}

func (s *savedAbortRouteHealthStore) ListRouteHealthHistory(_ context.Context, _, _, _ string, limit int, _ string) (api.RouteHealthHistoryPage, error) {
	return api.RouteHealthHistoryPage{Entries: s.newest[:min(limit, len(s.newest))]}, nil
}

// After the guard aborts a canary, the candidate serves nothing and its live
// windows read unknown; the release-guards GA drill saw exactly that. The
// report now shows the evidence the abort was saved with.
func TestRouteHealthReportShowsTheSavedAbort(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "health-saved-abort")
	app, _ := (*e.Store()).AppBySlug(t.Context(), slug)
	d, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate"})
	if err != nil {
		t.Fatal(err)
	}
	live := api.RouteHealthReport{AppID: app.ID, DeploymentID: d.ID, Mode: "enforce", OnRegression: "abort",
		Status: "unknown", Reason: "comparisons_incomplete_or_unsettled", Routes: []api.RouteHealthFinding{}}
	saved := live
	saved.Status, saved.Reason = "regressed", "server_error_regression"
	stub := &savedAbortRouteHealthStore{MemStore: (*e.Store()), live: live}
	(*(*e.S()).Store()) = statetest.NewMem(stub)
	read := func() api.RouteHealthReport {
		t.Helper()
		rec := e.Do(t, "GET", "/v1/apps/"+slug+"/route-health/deployments/"+d.ID, nil, nil)
		var report api.RouteHealthReport
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &report) != nil {
			t.Fatalf("report %d %s", rec.Code, rec.Body)
		}
		return report
	}
	if got := read(); got.Status != "unknown" {
		t.Fatalf("report without history = %s/%s, want the live report", got.Status, got.Reason)
	}
	stub.newest = []api.RouteHealthHistoryEntry{{Purpose: "abort", Report: saved,
		Decision: api.RouteHealthDecision{DeploymentID: d.ID, Status: "aborted", Reason: "consecutive_server_error_regression"}}}
	if got := read(); got.Status != "regressed" || got.Reason != "server_error_regression" {
		t.Fatalf("report after the abort = %s/%s, want the saved regression", got.Status, got.Reason)
	}
}
