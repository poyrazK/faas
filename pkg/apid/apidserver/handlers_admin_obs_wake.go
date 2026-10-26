package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

// obsWakeTimeline serves GET /v1/admin/obs/wakes/{wake_id}: every event of
// one wake, for an operator who has only the wake_id. Admin scope and MFA,
// like the other /v1/admin/obs reads.
func (s *server) obsWakeTimeline(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	wakeID := r.PathValue("wake_id")
	if !operatorobs.ValidWakeID(wakeID) || r.URL.RawQuery != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid wake timeline request", "wake_id must be 8-64 letters, digits or dashes, with no query parameters"))
		return
	}
	out, err := operatorobs.WakeTimeline(r.Context(), s.store, wakeID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the wake's events"))
		return
	}
	if len(out.Events) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Wake not found", "no events are recorded for this wake_id"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
