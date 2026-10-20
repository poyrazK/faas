package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsAlertTriageHistory(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), api.ObsAlertTriageHistoryLimitDefault, api.ObsAlertTriageHistoryLimitMax, "triage history")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	out, err := operatorobs.TriageHistory(r.Context(), s.store, r.PathValue("id"), r.URL.Query().Get("cursor"), limit, time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, state.ErrInvalidArgument):
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid triage history query", "use an alert episode ID and an unexpired cursor for the same episode"))
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Alert episode not retained", "triage history requires a retained episode"))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not load alert triage history"))
		}
		return
	}
	writeJSON(w, http.StatusOK, out)
}
