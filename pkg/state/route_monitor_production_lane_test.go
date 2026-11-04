package state_test

// adr: 20261028110044

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state"
)

// The stored monitor watches the app's production routing lane: a project's
// named production scope once it serves, the legacy default scope otherwise.
func TestProductionRouteMonitorFollowsProductionLane(t *testing.T) {
	pool, s, a, app, stable, candidate := productionMonitorPG(t)
	report := func() api.RouteMonitorReport {
		t.Helper()
		r, err := s.GetRouteMonitorReport(t.Context(), a.ID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := report(); r.DeploymentID != candidate.ID {
		t.Fatalf("default lane: monitored %q (%s), want %s", r.DeploymentID, r.Reason, candidate.ID)
	}
	productionMonitorExec(t, pool, "UPDATE deployments SET scope='production' WHERE id=$1", candidate.ID)
	if r := report(); r.DeploymentID != candidate.ID {
		t.Fatalf("project production lane: monitored %q (%s), want %s", r.DeploymentID, r.Reason, candidate.ID)
	}
	// A dark named production lane leaves the legacy default lane serving.
	productionMonitorExec(t, pool, "UPDATE deployments SET traffic_percent=CASE WHEN id=$1 THEN 100 ELSE 0 END WHERE app_id=$2", stable.ID, app.ID)
	if r := report(); r.DeploymentID != stable.ID {
		t.Fatalf("dark production lane: monitored %q (%s), want the default lane's %s", r.DeploymentID, r.Reason, stable.ID)
	}
	// A preview or staging scope is never production.
	productionMonitorExec(t, pool, "UPDATE deployments SET scope='staging' WHERE id=$1", candidate.ID)
	productionMonitorExec(t, pool, "UPDATE deployments SET traffic_percent=0 WHERE id=$1", stable.ID)
	productionMonitorExec(t, pool, "UPDATE deployments SET traffic_percent=100 WHERE id=$1", candidate.ID)
	if r := report(); r.DeploymentID != "" || r.Reason != "serving_deployment_ambiguous_or_missing" {
		t.Fatalf("staging scope monitored as production: %q (%s)", r.DeploymentID, r.Reason)
	}
}

// A project release rolls back as a set, so an incident on a project's
// production lane is skipped instead of rolling one member back.
func TestRouteMonitorRollbackSkipsProjectReleases(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	budget := int64(100)
	config := api.RouteMonitorConfig{Enabled: true, OnViolation: "rollback", Revision: 1}
	incident := api.RouteMonitorIncident{
		ID: "i", DeploymentID: "current", Revision: 1, Status: "open", OpenedAt: now.Add(-5 * time.Minute),
		Baseline: &api.RouteMonitorDeploymentBaseline{DeploymentID: "previous"},
		OpeningReport: api.RouteMonitorReport{Routes: []api.RouteMonitorFinding{
			{Route: api.RouteMonitorRoute{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget}, ErrorStatus: "violated"},
		}},
	}
	released := now.Add(-10 * time.Minute)
	for _, tc := range []struct {
		scope      string
		wantClaim  bool
		wantReason string
	}{
		{scope: "", wantClaim: true},
		{scope: api.DefaultEnvScope, wantClaim: true},
		{scope: "production", wantReason: routemonitor.RollbackSkipProjectRelease},
	} {
		i := incident
		d := state.Deployment{ID: "current", Scope: tc.scope, Status: state.DeployLive, TrafficPercent: 100, CreatedAt: released}
		_, claimed, changed := state.DecideRouteMonitorRollback(config, &i, d, now)
		if !changed || claimed != tc.wantClaim {
			t.Fatalf("scope %q: claimed=%v changed=%v, want claimed=%v", tc.scope, claimed, changed, tc.wantClaim)
		}
		if tc.wantReason != "" && (i.Rollback == nil || i.Rollback.Status != "skipped" || i.Rollback.Reason != tc.wantReason || i.Rollback.Route != "POST /checkout") {
			t.Fatalf("scope %q: rollback = %+v, want skipped %s", tc.scope, i.Rollback, tc.wantReason)
		}
	}
}
