package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getManagedPostgresDiagnostics(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	result, err := s.managedPostgres.Diagnose(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}
