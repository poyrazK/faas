package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/selectedtrace"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) selectedAppTrace(w http.ResponseWriter, r *http.Request, account state.Account) {
	limits := api.MustLimitsFor(account.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", account.Plan))
		return
	}
	selection, problem := selectedtrace.Parse(r.PathValue("trace_id"), r.URL.Query())
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	reader, ok := s.store.(state.SelectedTraceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("selected trace persistence is unavailable"))
		return
	}
	result, problem := selectedtrace.Lookup(r.Context(), s.store, reader, account.ID, selection, limits.DebugTelemetryRetentionDays)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
