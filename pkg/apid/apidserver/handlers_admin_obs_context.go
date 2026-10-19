package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsIncidentContext(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), api.ObsIncidentContextLimitDefault, api.ObsIncidentContextLimitMax, "related resources")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	prob, historyLimit := api.ParseLimit(r.URL.Query().Get("history_limit"), api.ObsIncidentEvidenceLimitDefault, api.ObsIncidentEvidenceLimitMax, "related history")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	out, err := operatorobs.Context(r.Context(), s.store, r.PathValue("kind"), r.PathValue("id"), r.URL.Query().Get("cursor"), limit, historyLimit, time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, state.ErrInvalidArgument):
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid incident context query", "use a valid incident reference and an unexpired cursor returned for that incident"))
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Incident resource not found", "resource is missing or deleted, or alert evidence is not retained"))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not load incident context"))
		}
		return
	}
	writeJSON(w, http.StatusOK, out)
}
