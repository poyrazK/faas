package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Retry is asynchronous; the worker repeats compatibility, readiness, smoke,
// and lifecycle checks. This endpoint cannot grant a migration override.
func (s *server) retryMCPTaskPromotionHold(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	var principal state.Deployment
	stampDeploymentActor(&principal, acct, r)
	actor := resolvedActorString(principal.DeployedVia, principal.DeployedByUserID, "")
	err := s.store.RetryMCPTaskPromotionRecovery(r.Context(), d.ID, actor)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such deployment")
		case errors.Is(err, state.ErrInvalidStateTransition):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "No retryable MCP promotion hold", "This deployment has no active queued compatibility hold. Inspect its current status."))
		default:
			writeCustomerInternalProblem(w, r, s.log, "request MCP promotion retry", "Gregale could not schedule this promotion retry.", "Retry the request in a moment.", err)
		}
		return
	}
	d, err = s.store.DeploymentByID(r.Context(), d.ID)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "read scheduled MCP retry", "The retry was scheduled, but Gregale could not refresh the deployment status.", "Inspect the deployment before retrying.", err)
		return
	}
	writeJSON(w, http.StatusAccepted, s.deploymentResponse(d, app))
}
