package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) setManagedPostgresRecoveryRegion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var req api.SetManagedPostgresRecoveryRegionRequest
	if err := decodeJSON(r, &req); err != nil || req.Region == nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	id := r.PathValue("id")
	if err := s.managedPostgres.SetRecoveryRegion(r.Context(), acct.ID, id, *req.Region); err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.recovery_region.updated", &acct.ID, map[string]any{"database_id": id, "recovery_region": *req.Region})
	w.WriteHeader(http.StatusNoContent)
}
