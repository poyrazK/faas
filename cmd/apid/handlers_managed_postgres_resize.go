package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func managedPostgresResizeView(operation managedpostgres.ResizeOperation) api.ManagedPostgresResize {
	out := api.ManagedPostgresResize{ID: operation.ID, DatabaseID: operation.DatabaseID, FromClass: string(operation.SourceSpec.Class),
		TargetClass: string(operation.TargetClass), Generation: operation.Generation, State: string(operation.State),
		ConnectionInterruptionExpected: true, LastErrorCode: operation.LastErrorCode, CreatedAt: operation.CreatedAt.UTC().Format(time.RFC3339Nano)}
	if !operation.CompletedAt.IsZero() {
		out.CompletedAt = operation.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func (s *server) admitManagedPostgresResize(ctx context.Context, account state.Account, database string, request api.ResizeManagedPostgresDatabaseRequest) error {
	// Existing accepted intents remain readable/replayable after a plan change.
	if _, err := s.managedPostgres.GetResize(ctx, account.ID, database, request.RequestID); err == nil {
		return nil
	} else if !errors.Is(err, managedpostgres.ErrNotFound) {
		return err
	}
	switch managedpostgres.ServiceClass(request.ServiceClass) {
	case managedpostgres.ClassDevelopment, managedpostgres.ClassBurstable, managedpostgres.ClassProduction:
	default:
		return managedpostgres.ErrInvalid
	}
	limits, ok := api.ManagedPostgresLimitsFor(account.Plan)
	if !ok || limits.DatabasesMax == 0 || !managedPostgresPlanAllows(limits, managedpostgres.Spec{Class: managedpostgres.ServiceClass(request.ServiceClass)}) {
		return managedpostgres.ErrQuotaExceeded
	}
	return nil
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
	operation, err := s.managedPostgres.Resize(r.Context(), managedpostgres.ResizeDatabaseRequest{AccountID: account.ID, DatabaseID: database,
		RequestID: request.RequestID, TargetClass: managedpostgres.ServiceClass(request.ServiceClass)})
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.database.resize_requested", &account.ID, map[string]any{"database_id": database, "request_id": operation.ID, "target_class": operation.TargetClass, "generation": operation.Generation})
	w.Header().Set("Location", "/v1/postgres/databases/"+url.PathEscape(database)+"/resizes/"+url.PathEscape(operation.ID))
	writeJSON(w, http.StatusAccepted, managedPostgresResizeView(operation))
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
	writeJSON(w, http.StatusOK, managedPostgresResizeView(operation))
}
