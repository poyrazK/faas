package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state"
)

// rollbackMonitorIncident opens a real error-budget incident on stable after a
// healthy baseline on candidate, with the failing release completed `released`
// ago.
func rollbackMonitorIncident(t *testing.T, released string) (*state.PgStore, state.Account, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	pool, s, a, app, stable, candidate := productionMonitorPG(t)
	config, err := s.GetRouteMonitor(t.Context(), a.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRouteMonitor(t.Context(), a.ID, app.ID, api.SetRouteMonitorRequest{Enabled: true, OnViolation: "rollback", ExpectedRevision: &config.Revision, Routes: config.Routes}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetRouteMonitor(t.Context(), a.ID, app.ID); err != nil || got.OnViolation != "rollback" {
		t.Fatalf("on_violation was not persisted: %+v %v", got, err)
	}
	productionMonitorExec(t, pool, "UPDATE route_monitors SET updated_at=clock_timestamp()-interval '3 hours' WHERE app_id=$1", app.ID)
	productionMonitorTraffic(t, pool, a, app, candidate.ID, "POST", "/checkout", 200, 100, 100, false)
	productionMonitorDue(t, pool, app.ID)
	productionMonitorEvaluate(t, s, a, app)

	productionMonitorExec(t, pool, "UPDATE deployments SET traffic_percent=CASE WHEN id=$1 THEN 100 ELSE 0 END,status=CASE WHEN id=$1 THEN 'live' ELSE status END,canary_step=canary_total_steps,rollout_state='complete',created_at=clock_timestamp()-interval '3 hours',canary_step_started_at=clock_timestamp()-$3::interval,rollout_completed_at=clock_timestamp()-$3::interval,commit_sha=$4 WHERE app_id=$2", stable.ID, app.ID, released, strings.Repeat("b", 40))
	productionMonitorTraffic(t, pool, a, app, stable.ID, "POST", "/checkout", 500, 100, 600, false)
	productionMonitorDue(t, pool, app.ID)
	productionMonitorEvaluate(t, s, a, app)
	return s, a, app, stable, candidate
}

// adr: 845
func TestRouteMonitorRollbackPostgresClaimsOnceAndRecords(t *testing.T) {
	s, a, app, stable, candidate := rollbackMonitorIncident(t, "5 minutes")
	claim, claimed, err := s.ClaimRouteMonitorRollback(t.Context(), a.ID, app.ID)
	if err != nil || !claimed {
		t.Fatalf("claim = %+v, %t, %v", claim, claimed, err)
	}
	if claim.DeploymentID != stable.ID || claim.TargetDeploymentID != candidate.ID || claim.Route != "POST /checkout" {
		t.Fatalf("claim = %+v, want %s -> %s for POST /checkout", claim, stable.ID, candidate.ID)
	}
	if _, again, err := s.ClaimRouteMonitorRollback(t.Context(), a.ID, app.ID); err != nil || again {
		t.Fatalf("second claim = %t, %v; want no claim", again, err)
	}
	operation := uuid.NewString()
	if err := s.RecordRouteMonitorRollback(t.Context(), a.ID, app.ID, claim.IncidentID, api.RouteMonitorIncidentRollback{Status: "requested", OperationID: operation, DecidedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	incident, err := s.GetRouteMonitorIncident(t.Context(), a.ID, app.ID, claim.IncidentID)
	if err != nil {
		t.Fatal(err)
	}
	rb := incident.Rollback
	if rb == nil || rb.Status != "requested" || rb.OperationID != operation || rb.TargetDeploymentID != candidate.ID || rb.Route != "POST /checkout" {
		t.Fatalf("recorded rollback = %+v", rb)
	}
	if err := routemonitor.ValidateIncident(incident, app.Slug); err != nil {
		t.Fatalf("incident with rollback failed validation: %v", err)
	}
	if err := s.RecordRouteMonitorRollback(t.Context(), a.ID, app.ID, claim.IncidentID, api.RouteMonitorIncidentRollback{Status: "skipped", DecidedAt: time.Now().UTC()}); err == nil {
		t.Fatal("a recorded decision was overwritten")
	}
}

// adr: 845
func TestRouteMonitorRollbackPostgresSkipsLateIncident(t *testing.T) {
	s, a, app, _, _ := rollbackMonitorIncident(t, "2 hours")
	if _, claimed, err := s.ClaimRouteMonitorRollback(t.Context(), a.ID, app.ID); err != nil || claimed {
		t.Fatalf("late incident claimed=%t err=%v", claimed, err)
	}
	page, err := s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 5, "")
	if err != nil || len(page.Incidents) != 1 {
		t.Fatalf("incidents = %+v, %v", page, err)
	}
	if rb := page.Incidents[0].Rollback; rb == nil || rb.Status != "skipped" || rb.Reason != routemonitor.RollbackSkipOutsideWindow {
		t.Fatalf("late incident rollback = %+v, want skipped outside the window", rb)
	}
}
