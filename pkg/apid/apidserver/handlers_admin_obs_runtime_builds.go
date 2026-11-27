package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsRuntimeBuilds(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	now := time.Now().UTC()
	selected, err := operatorobs.RuntimeBuildsSelection(r.URL.RawQuery, r.URL.ForceQuery, now)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid runtime build query", "requires a full release SHA, RFC3339 after boundary and an optional known expected_role"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	out, problem := s.runtimeBuildAdmission.Collect(r.Context(), s.promqlClient, s.store, selected, now, s.runtimeDiagnosticsMetrics)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
