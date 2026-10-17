package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsInputHealth(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	role, err := operatorobs.InputHealthSelection(r.URL.RawQuery, r.URL.ForceQuery)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid input health query", "only one nonempty known expected_role is accepted"))
		return
	}
	writeJSON(w, http.StatusOK, operatorobs.InputHealth(r.Context(), s.promqlClient, role, time.Now().UTC()))
}
