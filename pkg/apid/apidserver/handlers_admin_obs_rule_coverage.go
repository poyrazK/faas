package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsRuleCoverage(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	selected, err := operatorobs.RuleCoverageSelection(r.URL.RawQuery, r.URL.ForceQuery)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid rule coverage query", "only one nonempty known expected_set is accepted"))
		return
	}
	out := operatorobs.RuleCoverage(r.Context(), s.promqlClient, selected, time.Now().UTC())
	writeJSON(w, http.StatusOK, out)
}
