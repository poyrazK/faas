package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) compareAccountWorkflowReadiness(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowReadinessComparisonStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow readiness comparison unavailable"))
		return
	}
	var req api.OperationWorkflowReadinessComparisonRequest
	if !operation.DecodeOperationBody(w, r, &req, api.OperationWorkflowReadinessComparisonBodyMaxBytes) {
		return
	}
	out, err := store.CompareAccountWorkflowReadiness(r.Context(), acct.ID, app.ID, req)
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) comparePlatformTenantSelfWorkflowReadiness(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowReadinessComparisonStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow readiness comparison unavailable"))
		return
	}
	var req api.OperationWorkflowReadinessComparisonRequest
	if !operation.DecodeOperationBody(w, r, &req, api.OperationWorkflowReadinessComparisonBodyMaxBytes) {
		return
	}
	out, err := store.ComparePlatformTenantWorkflowReadiness(r.Context(), acct.ID, tenant, req)
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
