package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) rotateManagedPostgresBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgresBindings == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	binding, err := s.managedPostgresBindings.Rotate(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	app, err := s.store.AppByID(r.Context(), binding.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "app not found")
		return
	}
	// The service owns the durable handoff. A concurrent scheduler completion
	// may already have cleared the rotation metadata on an idempotent retry.
	s.audit.Emit(r.Context(), "managed_postgres.binding.rotated", &acct.ID, map[string]any{
		"binding_id": binding.ID, "database_id": binding.DatabaseID,
		"app_id": binding.AppID, "credential_generation": binding.CredentialGeneration,
		"wake_id": binding.RotationWakeID,
	})
	writeJSON(w, http.StatusOK, managedPostgresBindingView(binding))
}
