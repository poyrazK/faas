package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// managedPostgresSuspendTimeoutsOrLog reads the suspend timeout in force for
// each scale-to-zero database; a failed read leaves the field out of the view.
func (s *server) managedPostgresSuspendTimeoutsOrLog(ctx context.Context, databases []managedpostgres.Database) map[string]managedpostgres.SuspendTimeout {
	if s.managedPostgres == nil {
		return nil
	}
	timeouts, err := s.managedPostgres.SuspendTimeouts(ctx, databases)
	if err != nil {
		s.log.Warn("managed postgres suspend timeout: read", "err", err)
		return nil
	}
	return timeouts
}

func withManagedPostgresSuspendTimeout(view *api.ManagedPostgresDatabase, timeouts map[string]managedpostgres.SuspendTimeout) {
	if timeout, ok := timeouts[view.ID]; ok && view.ScaleToZero {
		view.SuspendTimeoutSeconds, view.SuspendTimeoutCustom = timeout.Seconds, timeout.Custom
	}
}

// setManagedPostgresSuspendTimeout answers PUT
// /v1/postgres/databases/{id}/suspend-timeout: how long a scale-to-zero
// database stays up after its last activity, or null for the default.
func (s *server) setManagedPostgresSuspendTimeout(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var request api.SetManagedPostgresSuspendTimeoutRequest
	if err := decodeJSON(r, &request); err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	if request.Seconds != nil && !managedpostgres.ValidCustomerSuspendTimeout(*request.Seconds) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "invalid_suspend_timeout", "Invalid suspend timeout",
			"seconds must be between 60 and 3600, or null for the default"))
		return
	}
	limits, ok := api.ManagedPostgresLimitsFor(account.Plan)
	if !ok || limits.DatabasesMax == 0 {
		managedPostgresProblem(w, managedpostgres.ErrNotInPlan)
		return
	}
	database := r.PathValue("id")
	timeout, err := s.managedPostgres.SetSuspendTimeout(r.Context(), account.ID, database, request.Seconds)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.database.suspend_timeout_set", &account.ID,
		map[string]any{"database_id": timeout.DatabaseID, "seconds": timeout.Seconds, "custom": timeout.Custom})
	writeJSON(w, http.StatusOK, api.ManagedPostgresSuspendTimeout{DatabaseID: timeout.DatabaseID, Seconds: timeout.Seconds,
		Custom: timeout.Custom, Applied: timeout.Applied})
}
