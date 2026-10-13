package apidserver

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/managedpg"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) admitManagedPostgresResize(ctx context.Context, account state.Account, database string, request api.ResizeManagedPostgresDatabaseRequest) error {
	// Existing accepted intents remain readable/replayable after a plan change.
	if _, err := s.managedPostgres.GetResize(ctx, account.ID, database, request.RequestID); err == nil {
		return nil
	} else if !errors.Is(err, managedpostgres.ErrNotFound) {
		return err
	}
	return managedpg.AdmitResize(account.Plan, request)
}

func (s *server) resizeManagedPostgresDatabase(w http.ResponseWriter, r *http.Request, account state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var request api.ResizeManagedPostgresDatabaseRequest
	if err := decodeJSON(r, &request); err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	database := r.PathValue("id")
	if err := s.admitManagedPostgresResize(r.Context(), account, database, request); err != nil {
		managedPostgresProblem(w, err)
		return
	}
	operation, err := s.managedPostgres.Resize(r.Context(), managedpg.ResizeRequest(account.ID, database, request))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	view := managedpg.ResizeView(operation)
	s.audit.Emit(r.Context(), "managed_postgres.database.resize_requested", &account.ID, map[string]any{"database_id": database, "request_id": operation.ID,
		"target_class": view.TargetClass, "target_storage_limit_bytes": view.TargetStorageLimitBytes, "generation": operation.Generation})
	w.Header().Set("Location", "/v1/postgres/databases/"+url.PathEscape(database)+"/resizes/"+url.PathEscape(operation.ID))
	writeJSON(w, http.StatusAccepted, view)
}

func (s *server) getManagedPostgresResize(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	operation, err := s.managedPostgres.GetResize(r.Context(), account.ID, r.PathValue("id"), r.PathValue("resize_id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, managedpg.ResizeView(operation))
}
