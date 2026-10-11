package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state"
)

// routeMonitorRollbackActor attributes automatic rollbacks to the monitor,
// never to a customer request.
const routeMonitorRollbackActor = "apid:route_monitor_rollback"

// applyRouteMonitorRollback requests at most one checked rollback for an app's
// active error-budget incident when its monitor opts into on_violation
// rollback (ADR-845). The checked rollback path owns every deployment write
// and rechecks that the incident's deployment still serves all traffic with no
// rollout or other rollback in its scope.
func (s *server) applyRouteMonitorRollback(ctx context.Context, target state.RouteMonitorTarget) {
	store, ok := s.store.(state.RouteMonitorRollbackStore)
	if !ok {
		return
	}
	claim, claimed, err := store.ClaimRouteMonitorRollback(ctx, target.AccountID, target.AppID)
	if err != nil {
		s.log.Warn("route monitor rollback claim failed", "app_id", target.AppID, "err", err)
		return
	}
	if !claimed {
		return
	}
	outcome := api.RouteMonitorIncidentRollback{Status: "skipped", Reason: routemonitor.RollbackSkipTargetIneligible, DecidedAt: time.Now().UTC()}
	defer func() {
		if err := store.RecordRouteMonitorRollback(ctx, target.AccountID, target.AppID, claim.IncidentID, outcome); err != nil {
			s.log.Warn("route monitor rollback outcome not recorded", "app_id", target.AppID, "incident_id", claim.IncidentID, "err", err)
		}
	}()
	acct, err := s.store.AccountByID(ctx, target.AccountID)
	if err != nil {
		return
	}
	app, err := s.store.AppByID(ctx, target.AppID)
	if err != nil || app.AccountID != acct.ID {
		return
	}
	reason := "route monitor incident " + claim.IncidentID + ": " + claim.Route + " error budget violated"
	if len(reason) > api.BindingReleasePolicyReasonMaxBytes {
		reason = "route monitor incident " + claim.IncidentID + ": error budget violated"
	}
	targetID, currentID := claim.TargetDeploymentID, claim.DeploymentID
	operation, prepared, problem := s.prepareCheckedRollback(ctx, acct, app, api.RollbackRequest{
		TargetDeploymentID: &targetID, ExpectedCurrentDeploymentID: &currentID, Reason: reason,
	})
	if problem != nil {
		s.log.Warn("route monitor rollback not requested", "app_id", app.ID, "incident_id", claim.IncidentID, "code", problem.Code)
		return
	}
	outcome = api.RouteMonitorIncidentRollback{Status: "requested", OperationID: operation.ID, DecidedAt: time.Now().UTC()}
	if s.notif != nil {
		payload, _ := json.Marshal(map[string]string{"app_id": app.ID, "deployment_id": prepared.ID})
		if err := s.notif.Notify(ctx, db.NotifySnapshotPrime, string(payload)); err != nil {
			s.log.Warn("route monitor rollback readiness wakeup", "request_id", operation.ID, "err", err)
		}
	}
	s.audit.Emit(ctx, "route_monitor.rollback_requested", &acct.ID, map[string]any{
		"app_id": app.ID, "incident_id": claim.IncidentID, "route": claim.Route, "actor": routeMonitorRollbackActor,
		"from_deployment_id": currentID, "to_deployment_id": targetID, "operation_id": operation.ID,
	})
}
