package apidserver_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state"
)

// routeMonitorRollbackFixture seeds an open incident whose healthy baseline
// is the fixture's rollback target, or baselineID when given.
func routeMonitorRollbackFixture(t *testing.T, onViolation string, openedAfterRelease time.Duration, baselineID ...string) (apidserver.TestEnv, state.App, state.Deployment, state.Deployment, string) {
	t.Helper()
	e, app, target, current := apidserver.CheckedRollbackAPIFixture(t)
	zero, budget := int64(0), int64(100)
	if _, err := (*e.Store()).SetRouteMonitor(t.Context(), (*e.Acct()).ID, app.ID, api.SetRouteMonitorRequest{
		Enabled: true, OnViolation: onViolation, ExpectedRevision: &zero,
		Routes: []api.RouteMonitorRoute{{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget}},
	}); err != nil {
		t.Fatal(err)
	}
	current, err := (*e.Store()).DeploymentByID(t.Context(), current.ID)
	if err != nil {
		t.Fatal(err)
	}
	released := routemonitor.ReleasedAt(current.CreatedAt, current.CanaryStepStartedAt, current.RolloutCompletedAt)
	baseline := target.ID
	if len(baselineID) > 0 {
		baseline = baselineID[0]
	}
	incident := api.RouteMonitorIncident{
		Version: api.RouteMonitorVersion, ID: uuid.NewString(), AppID: app.ID, DeploymentID: current.ID, Revision: 1,
		Status: "open", OpenedAt: released.Add(openedAfterRelease), Baseline: &api.RouteMonitorDeploymentBaseline{DeploymentID: baseline},
		OpeningReport: api.RouteMonitorReport{AppID: app.ID, DeploymentID: current.ID, Status: "violated", Routes: []api.RouteMonitorFinding{{
			Route: api.RouteMonitorRoute{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget}, Status: "violated", ErrorStatus: "violated",
		}}},
	}
	(*e.Store()).SeedRouteMonitorIncidentForTest(app.ID, incident)
	return e, app, target, current, incident.ID
}

// adr: 845
func TestRouteMonitorRollbackRequestsCheckedRollbackOnce(t *testing.T) {
	e, app, target, current, incidentID := routeMonitorRollbackFixture(t, "rollback", time.Minute)
	ctx := t.Context()
	monitor := state.RouteMonitorTarget{AccountID: (*e.Acct()).ID, AppID: app.ID}
	(*e.S()).ApplyRouteMonitorRollback(ctx, monitor)

	incident, err := (*e.Store()).GetRouteMonitorIncident(ctx, (*e.Acct()).ID, app.ID, incidentID)
	if err != nil {
		t.Fatal(err)
	}
	rb := incident.Rollback
	if rb == nil || rb.Status != "requested" || rb.OperationID == "" || rb.TargetDeploymentID != target.ID || rb.Route != "POST /checkout" {
		t.Fatalf("incident rollback = %+v, want requested to %s", rb, target.ID)
	}
	operation, err := (*e.Store()).GetCheckedRollback(ctx, (*e.Acct()).ID, app.ID, rb.OperationID)
	if err != nil || operation.TargetDeploymentID != target.ID || operation.CurrentDeploymentID != current.ID || operation.Status != "preparing" {
		t.Fatalf("checked rollback = %+v, %v", operation, err)
	}
	(*e.S()).ApplyRouteMonitorRollback(ctx, monitor)
	again, err := (*e.Store()).GetRouteMonitorIncident(ctx, (*e.Acct()).ID, app.ID, incidentID)
	if err != nil || again.Rollback == nil || again.Rollback.OperationID != rb.OperationID {
		t.Fatalf("second pass changed the decision: %+v %v", again.Rollback, err)
	}
}

// adr: 845
func TestRouteMonitorRollbackSkipsOrIgnores(t *testing.T) {
	t.Run("incident outside the post-release window", func(t *testing.T) {
		e, app, _, _, incidentID := routeMonitorRollbackFixture(t, "rollback", api.RouteMonitorRollbackWindow+time.Minute)
		ctx := t.Context()
		(*e.S()).ApplyRouteMonitorRollback(ctx, state.RouteMonitorTarget{AccountID: (*e.Acct()).ID, AppID: app.ID})
		incident, err := (*e.Store()).GetRouteMonitorIncident(ctx, (*e.Acct()).ID, app.ID, incidentID)
		if err != nil || incident.Rollback == nil || incident.Rollback.Status != "skipped" || incident.Rollback.Reason != routemonitor.RollbackSkipOutsideWindow {
			t.Fatalf("late incident rollback = %+v, %v", incident.Rollback, err)
		}
	})
	// adr: 20261028110044 — a refused checked rollback records its problem code.
	t.Run("checked rollback refuses", func(t *testing.T) {
		e, app, _, _, incidentID := routeMonitorRollbackFixture(t, "rollback", time.Minute, uuid.NewString())
		ctx := t.Context()
		(*e.S()).ApplyRouteMonitorRollback(ctx, state.RouteMonitorTarget{AccountID: (*e.Acct()).ID, AppID: app.ID})
		incident, err := (*e.Store()).GetRouteMonitorIncident(ctx, (*e.Acct()).ID, app.ID, incidentID)
		if err != nil || incident.Rollback == nil || incident.Rollback.Status != "skipped" || incident.Rollback.Reason != routemonitor.RollbackSkipTargetIneligible || incident.Rollback.RefusalCode == "" {
			t.Fatalf("refused rollback = %+v, %v; want skipped with the refusal code", incident.Rollback, err)
		}
	})
	t.Run("report mode", func(t *testing.T) {
		e, app, _, _, incidentID := routeMonitorRollbackFixture(t, "", time.Minute)
		ctx := t.Context()
		(*e.S()).ApplyRouteMonitorRollback(ctx, state.RouteMonitorTarget{AccountID: (*e.Acct()).ID, AppID: app.ID})
		incident, err := (*e.Store()).GetRouteMonitorIncident(ctx, (*e.Acct()).ID, app.ID, incidentID)
		if err != nil || incident.Rollback != nil {
			t.Fatalf("report-mode incident rollback = %+v, %v", incident.Rollback, err)
		}
	})
}
