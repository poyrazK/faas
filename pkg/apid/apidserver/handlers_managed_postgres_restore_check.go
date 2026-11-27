package apidserver

import (
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) checkManagedPostgresRestore(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	point, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("point_in_time"))
	if err != nil {
		managedPostgresProblem(w, fmt.Errorf("%w: point_in_time must be an RFC3339 timestamp", managedpostgres.ErrInvalid))
		return
	}
	check, err := s.managedPostgres.CheckRestore(r.Context(), acct.ID, r.PathValue("id"), point)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, check)
}
