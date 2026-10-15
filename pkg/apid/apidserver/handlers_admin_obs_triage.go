package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) postObsAlertTriage(w http.ResponseWriter, r *http.Request, caller state.Account) {
	var req api.ObsAlertTriageRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid triage request", "provide action, expected_revision and snoozed_until only for snooze"))
		return
	}
	out, err := operatorobs.MutateTriage(r.Context(), s.store, r.PathValue("id"), caller.ID, req, middleware.TraceIDFrom(r), time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, state.ErrInvalidArgument):
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid triage request", "use a retained episode ID, a supported action, a nonnegative expected_revision, and a snooze deadline within 24 hours"))
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Alert episode not retained", "use the immutable episode ID returned in incident.id; series aliases cannot be mutated"))
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, "conflict", "Triage changed or owned by another operator", "refresh evidence and its revision; only the current owner can release ownership"))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not update alert triage"))
		}
		return
	}
	writeJSON(w, http.StatusOK, out)
}
