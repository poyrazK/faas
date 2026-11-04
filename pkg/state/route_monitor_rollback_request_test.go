package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRouteMonitorRollbackRequestIsAtomicAndReplayableMem(t *testing.T) {
	s := state.NewMemStore()
	a, app, target, current := checkedRollbackFixture(t, s, false)
	zero, budget := int64(0), int64(100)
	c, err := s.SetRouteMonitor(t.Context(), a.ID, app.ID, api.SetRouteMonitorRequest{Enabled: true, OnViolation: "rollback", ExpectedRevision: &zero,
		Routes: []api.RouteMonitorRoute{{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget}}})
	if err != nil {
		t.Fatal(err)
	}
	i := api.RouteMonitorIncident{Version: api.RouteMonitorVersion, ID: uuid.NewString(), AppID: app.ID, DeploymentID: current.ID, Revision: c.Revision, Status: "open",
		OpenedAt: routemonitor.ReleasedAt(current.CreatedAt, current.RolloutCompletedAt).Add(time.Minute), Baseline: &api.RouteMonitorDeploymentBaseline{DeploymentID: target.ID},
		OpeningReport: api.RouteMonitorReport{Status: "violated", Routes: []api.RouteMonitorFinding{{Route: c.Routes[0], Status: "violated", ErrorStatus: "violated"}}}}
	s.SeedRouteMonitorIncidentForTest(app.ID, i)
	claim, ok, err := s.ClaimRouteMonitorRollback(t.Context(), a.ID, app.ID)
	if err != nil || !ok {
		t.Fatalf("claim: %+v %v %v", claim, ok, err)
	}
	ctx := state.WithRouteMonitorRollbackClaim(t.Context(), claim)
	first, err := s.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, current.ID, "monitor request")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, current.ID, "monitor request")
	if err != nil || again.ID != first.ID {
		t.Fatalf("claim created a second operation: %+v %v", again, err)
	}
	i, err = s.GetRouteMonitorIncident(t.Context(), a.ID, app.ID, i.ID)
	if err != nil || i.Rollback == nil || i.Rollback.OperationID != first.ID || i.Rollback.Status != "requested" {
		t.Fatalf("incident and intent were not linked: %+v %v", i.Rollback, err)
	}
	if _, ok, err := s.ClaimRouteMonitorRollback(t.Context(), a.ID, app.ID); err != nil || ok {
		t.Fatalf("linked claim was repeated: %v %v", ok, err)
	}
}

func TestRouteMonitorRollbackRequestIsAtomicAndReplayablePG(t *testing.T) {
	pool, s, a, app, current, target := rollbackMonitorIncident(t, "5 minutes")
	claim, ok, err := s.ClaimRouteMonitorRollback(t.Context(), a.ID, app.ID)
	if err != nil || !ok {
		t.Fatalf("claim: %+v %v %v", claim, ok, err)
	}
	ctx := state.WithRouteMonitorRollbackClaim(t.Context(), claim)
	before, err := s.DeploymentByID(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Fail after checked intent and readiness delivery have been written,
	// precisely when the incident is about to be linked in that transaction.
	productionMonitorExec(t, pool, "CREATE FUNCTION reject_monitor_link() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.entry->'rollback'->>'status'='requested' THEN RAISE EXCEPTION 'injected incident link failure'; END IF; RETURN NEW; END $$")
	productionMonitorExec(t, pool, "CREATE TRIGGER reject_monitor_link BEFORE INSERT OR UPDATE ON route_monitor_incidents FOR EACH ROW EXECUTE FUNCTION reject_monitor_link()")
	if _, err := s.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, current.ID, "monitor request"); err == nil {
		t.Fatal("injected late link failure was ignored")
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM deployment_rollback_operations WHERE app_id=$1", app.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed link orphaned an operation: %d %v", count, err)
	}
	after, err := s.DeploymentByID(ctx, target.ID)
	if err != nil || after.Status != before.Status || after.TrafficPercent != before.TrafficPercent {
		t.Fatalf("failed link prepared its target: %+v %v", after, err)
	}
	i, err := s.GetRouteMonitorIncident(ctx, a.ID, app.ID, claim.IncidentID)
	if err != nil || i.Rollback == nil || i.Rollback.Status != "claimed" {
		t.Fatalf("failed link consumed its claim: %+v %v", i.Rollback, err)
	}
	productionMonitorExec(t, pool, "DROP TRIGGER reject_monitor_link ON route_monitor_incidents")
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	type result struct {
		operation api.RollbackOperation
		err       error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			r, err := s.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, current.ID, "monitor request")
			results <- result{r, err}
		}()
	}
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.operation.ID != second.operation.ID {
		t.Fatalf("concurrent retries duplicated or lost intent: %+v %+v", first, second)
	}
	i, err = s.GetRouteMonitorIncident(ctx, a.ID, app.ID, claim.IncidentID)
	if err != nil || i.Rollback == nil || i.Rollback.OperationID != first.operation.ID || i.Rollback.Status != "requested" {
		t.Fatalf("incident is not linked to the committed operation: %+v %v", i.Rollback, err)
	}
}
