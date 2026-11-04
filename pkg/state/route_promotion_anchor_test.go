package state_test

// adr: 20261028110044

import (
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state"
)

// A traffic change that takes a deployment to 100% records when, as a
// --no-traffic release's promotion does; a partial split or an unchanged 100%
// does not (amendment 4).
func TestManualPromotionStampsPromotedAt(t *testing.T) {
	pool, s, _, _, stable, candidate := productionMonitorPG(t)
	promotedAt := func(id string) *time.Time {
		t.Helper()
		var at *time.Time
		if err := pool.QueryRow(t.Context(), "SELECT promoted_at FROM deployments WHERE id=$1", id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	before := time.Now().Add(-time.Minute)
	if _, err := s.UpdateDeploymentTraffic(t.Context(), stable.ID, 50); err != nil {
		t.Fatal(err)
	}
	if at := promotedAt(stable.ID); at != nil {
		t.Fatalf("a 50%% split stamped promoted_at %v", at)
	}
	if _, err := s.UpdateDeploymentTraffic(t.Context(), stable.ID, 100); err != nil {
		t.Fatal(err)
	}
	stamped := promotedAt(stable.ID)
	if stamped == nil || stamped.Before(before) {
		t.Fatalf("promotion to 100%% stamped %v", stamped)
	}
	if at := promotedAt(candidate.ID); at != nil {
		t.Fatalf("the demoted deployment was stamped %v", at)
	}
	if _, err := s.UpdateDeploymentTraffic(t.Context(), stable.ID, 100); err != nil {
		t.Fatal(err)
	}
	if at := promotedAt(stable.ID); at == nil || !at.Equal(*stamped) {
		t.Fatalf("an unchanged 100%% moved promoted_at from %v to %v", stamped, at)
	}
}

// A release created hours ago and promoted minutes ago is inside the
// early-incident rollback window (amendment 4).
func TestRouteMonitorRollbackAnchorsOnPromotion(t *testing.T) {
	pool, s, a, app, stable, candidate := rollbackMonitorIncident(t, "2 hours")
	productionMonitorExec(t, pool, "UPDATE deployments SET promoted_at=clock_timestamp()-interval '5 minutes' WHERE id=$1", stable.ID)
	claim, claimed, err := s.ClaimRouteMonitorRollback(t.Context(), a.ID, app.ID)
	if err != nil || !claimed || claim.DeploymentID != stable.ID || claim.TargetDeploymentID != candidate.ID {
		t.Fatalf("claim = %+v, %t, %v; want a rollback of the recently promoted release", claim, claimed, err)
	}

	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	budget := int64(100)
	config := api.RouteMonitorConfig{Enabled: true, OnViolation: "rollback", Revision: 1}
	incident := api.RouteMonitorIncident{
		ID: "i", DeploymentID: "current", Revision: 1, Status: "open", OpenedAt: now.Add(-time.Minute),
		Baseline: &api.RouteMonitorDeploymentBaseline{DeploymentID: "previous"},
		OpeningReport: api.RouteMonitorReport{Routes: []api.RouteMonitorFinding{
			{Route: api.RouteMonitorRoute{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget}, ErrorStatus: "violated"},
		}},
	}
	d := state.Deployment{ID: "current", Status: state.DeployLive, TrafficPercent: 100, CreatedAt: now.Add(-3 * time.Hour)}
	late := incident
	if _, claimed, _ := state.DecideRouteMonitorRollback(config, &late, d, nil, now); claimed || late.Rollback == nil || late.Rollback.Reason != routemonitor.RollbackSkipOutsideWindow {
		t.Fatalf("unpromoted release: claimed=%v rollback=%+v, want skipped outside the window", claimed, late.Rollback)
	}
	promoted := now.Add(-5 * time.Minute)
	if _, claimed, _ := state.DecideRouteMonitorRollback(config, &incident, d, &promoted, now); !claimed {
		t.Fatalf("promoted release: rollback = %+v, want claimed", incident.Rollback)
	}
}

// The gateway reserves labels for every route the guard or the monitor
// selects, once each (amendment 4). productionMonitorPG's monitor selects
// POST /checkout.
func TestRouteLabelPinsUnionGuardAndMonitor(t *testing.T) {
	_, s, a, app, _, _ := productionMonitorPG(t)
	zero := int64(0)
	if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/items"}, {Method: "POST", Path: "/checkout"}}}); err != nil {
		t.Fatal(err)
	}
	labels, err := s.RouteLabelPins(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET /items", "POST /checkout"}; !slices.Equal(labels, want) {
		t.Fatalf("pins = %v, want %v with the monitor's POST /checkout counted once", labels, want)
	}
	if other, err := s.RouteLabelPins(t.Context(), a.ID); err != nil || len(other) != 0 {
		t.Fatalf("pins for an id that is no app = %v, %v", other, err)
	}
}
