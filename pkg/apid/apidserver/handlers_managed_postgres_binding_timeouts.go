package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getManagedPostgresBindingTimeouts(w http.ResponseWriter, r *http.Request, account state.Account) {
	if s.managedPostgresBindings == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	status, err := s.managedPostgresBindings.BindingTimeouts(r.Context(), account.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, status)
}

func (s *server) setManagedPostgresBindingTimeouts(w http.ResponseWriter, r *http.Request, account state.Account) {
	if s.managedPostgresBindings == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var request managedpostgres.BindingTimeoutPatch
	if err := decodeJSON(r, &request); err != nil || request.Validate() != nil {
		api.WriteProblem(w, api.ErrValidation("supply at least one timeout between 0 and 2147483647 milliseconds"))
		return
	}
	status, err := s.managedPostgresBindings.SetBindingTimeouts(r.Context(), account.ID, r.PathValue("id"), request)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.binding.timeouts_updated", &account.ID, map[string]any{
		"binding_id": status.BindingID, "revision": status.Revision,
		"statement_timeout_ms": status.StatementMS, "lock_timeout_ms": status.LockMS,
		"idle_in_transaction_session_timeout_ms": status.IdleInTransactionMS,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, status)
}
