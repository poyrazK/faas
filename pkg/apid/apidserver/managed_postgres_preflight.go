package apidserver

import (
	"net/http"

	postgresapi "github.com/onebox-faas/faas/pkg/apid/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) managedPostgresPreflight(w http.ResponseWriter, r *http.Request, acct state.Account) {
	h := postgresapi.PreflightHandler{Service: s.managedPostgresCutovers, Problem: managedPostgresProblem,
		Audit: func(r *http.Request, acct state.Account, id string) {
			s.audit.Emit(r.Context(), "managed_postgres.cutover.preflight_requested", &acct.ID, map[string]any{"cutover_id": id})
		}}
	h.Serve(w, r, acct)
}
