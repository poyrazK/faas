package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) requestPinnedServiceAbort(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, candidate, predecessor state.Deployment, reason string) {
	store, ok := s.store.(state.ServiceRolloutBindingStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("service rollout recovery unavailable"))
		return
	}
	updated, auditID, err := store.RequestServiceRolloutAbort(r.Context(), app.ID, candidate.ID, predecessor.ID, reason)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrServiceRolloutInvalid):
			api.WriteProblem(w, api.ErrRolloutStateInvalid("current"))
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such active service rollout")
		default:
			writeCustomerInternalProblem(w, r, s.log, "request service rollout abort", "Gregale could not request this recovery.", "Retry after checking the exact deployment and predecessor.", err)
		}
		return
	}
	gate := updated.ServiceRolloutHandoff.BindingsCheck
	if s.audit != nil {
		s.audit.Emit(r.Context(), "deployment.rollout_recovery_requested", &acct.ID, map[string]any{
			"app": app.ID, "deployment": updated.ID, "expected_predecessor": predecessor.ID,
			"request_id": gate.RequestID, "reason": updated.ServiceRolloutHandoff.Reason,
			"deployment_audit": auditID, "actor": acct.ID,
		})
	}
	if s.notif != nil {
		payload, _ := json.Marshal(map[string]string{"kind": "service_rollout_abort", "app_id": app.ID, "deployment_id": updated.ID, "status": string(updated.Status)})
		if err := s.notif.Notify(r.Context(), db.NotifyDeploymentChanged, string(payload)); err != nil {
			s.log.Warn("notify exact service abort", "deployment_id", updated.ID, "err", err)
		}
	}
	writeJSON(w, http.StatusAccepted, api.RolloutTransitionResponse{Deployment: s.deploymentResponse(updated, app), AuditID: int64ToAuditIDString(auditID), ServiceRecovery: &api.ServiceRolloutRecoveryReceipt{DeploymentID: updated.ID, PredecessorDeploymentID: predecessor.ID, RequestID: gate.RequestID, Status: "accepted"}})
}
