package apidserver

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getEnvironmentRouteBudgetReceipt(w http.ResponseWriter, r *http.Request, a state.Account) {
	requestID := r.PathValue("request_id")
	id, err := uuid.Parse(requestID)
	if err != nil || id == uuid.Nil || id.String() != requestID {
		api.WriteProblem(w, api.ErrValidation("request_id must be a canonical nonzero UUID"))
		return
	}
	app, env, scope, _, ok := s.environmentBudgetTarget(w, r, a)
	if !ok {
		return
	}
	store, ok := s.store.(state.EnvironmentRouteBudgetReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("scoped budget receipts unavailable"))
		return
	}
	out, err := store.GetEnvironmentRouteBudgetReceipt(r.Context(), a.ID, app.ID, scope, requestID)
	if err != nil {
		s.environmentBudgetError(w, err, a)
		return
	}
	if out.AppID != app.ID || out.Scope != scope || out.EnvironmentID != env.ID || out.ProjectID != env.ProjectID || out.RequestID != requestID {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Environment changed", "Inspect again"))
		return
	}
	if problem := s.scopedTelemetryIdentity(r.Context(), a, app, env); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
