package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsAlertWorkload(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if r.URL.RawQuery != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Summary does not accept query parameters", "the scope is all retained platform alert episodes"))
		return
	}
	enabled := s.promqlClient != nil && s.promqlClient.BaseURL() != ""
	out, err := operatorobs.AlertWorkloadSummary(r.Context(), s.store, enabled, time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load alert workload summary"))
		return
	}
	writeJSON(w, http.StatusOK, out)
}
