package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsRuleHealth(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid rule health query", "rule health accepts no query parameters"))
		return
	}
	out := operatorobs.RuleHealth(r.Context(), s.promqlClient, time.Now().UTC())
	writeJSON(w, http.StatusOK, out)
}
