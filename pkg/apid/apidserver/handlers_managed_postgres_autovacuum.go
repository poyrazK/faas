package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) managedPostgresAutovacuum(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var req api.SetManagedPostgresAutovacuumRequest
	if r.Method == http.MethodPut {
		if err := decodeJSONSized(r, &req, 4<<10); err != nil {
			api.WriteProblem(w, api.ErrValidation("expected a valid autovacuum profile"))
			return
		}
		if _, ok := req.Profile.Settings(); !ok {
			api.WriteProblem(w, api.ErrValidation("profile must be default or high-churn"))
			return
		}
	}
	out, err := s.managedPostgres.Autovacuum(r.Context(), acct.ID, r.PathValue("id"), req.Profile)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
