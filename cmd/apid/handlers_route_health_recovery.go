package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Only the loopback action credential can request this fresh check. Customer
// advances keep their existing behavior, including health holds and evidence.
func (s *server) recoverCanaryRouteHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CanaryRouteHealthRecoveryRequest
	if err := decodeJSON(r, &req); err != nil || req.ExpectedStep < 0 {
		api.WriteProblem(w, api.ErrValidation("supply a nonnegative expected_step"))
		return
	}
	d, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.RouteHealthRecoveryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route health recovery unavailable"))
		return
	}
	result, err := store.RecoverCanaryRouteHealth(r.Context(), acct.ID, app.ID, d.ID, req.ExpectedStep)
	if !s.writeCanaryAdvanceError(r.Context(), w, err, d.ID, req.ExpectedStep, d.CanaryStep) {
		return
	}
	if result.Aborted {
		if result.Decision == nil || result.AuditID == 0 {
			api.WriteProblem(w, api.ErrCapacity("route health recovery result unavailable"))
			return
		}
		if s.audit != nil {
			s.audit.Emit(r.Context(), "route_health.aborted", &acct.ID, map[string]any{"app_id": app.ID, "deployment_id": d.ID, "decision_id": result.Decision.HistoryID, "deployment_audit_id": result.AuditID})
		}
	}
	writeJSON(w, http.StatusOK, api.CanaryRouteHealthRecoveryResponse{Aborted: result.Aborted, RouteHealth: result.Decision, AuditID: int64ToAuditIDString(result.AuditID)})
}

func (s *server) internalRecoverCanaryRouteHealth(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.DeploymentByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.internalSafeDeployLookupError(w, r, err, "deployment")
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil {
		s.internalSafeDeployLookupError(w, r, err, "deployment")
		return
	}
	acct, err := s.store.AccountByID(r.Context(), app.AccountID)
	if err != nil {
		s.internalSafeDeployLookupError(w, r, err, "deployment")
		return
	}
	// Every check evaluates fresh observations. An idempotency response cache
	// would freeze a healthy or sparse verdict and suppress a later regression.
	s.recoverCanaryRouteHealth(w, r, acct)
}
