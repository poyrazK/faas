package apidserver

import (
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) managedPostgresExtensions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var major int
	if values, supplied := r.URL.Query()["target_major"]; supplied {
		if len(values) != 1 {
			managedPostgresProblem(w, managedpostgres.ErrInvalid)
			return
		}
		var err error
		major, err = strconv.Atoi(values[0])
		if err != nil || major <= 0 {
			managedPostgresProblem(w, managedpostgres.ErrInvalid)
			return
		}
	}
	name := r.PathValue("extension")
	report, err := s.managedPostgres.Extensions(r.Context(), acct.ID, r.PathValue("id"), name, major)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	if name != "" {
		s.audit.Emit(r.Context(), "managed_postgres.extension.enabled", &acct.ID, map[string]any{
			"database_id": r.PathValue("id"), "extension": name,
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, report)
}
