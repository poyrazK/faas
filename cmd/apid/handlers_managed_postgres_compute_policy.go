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

func managedPostgresComputePolicyView(operation managedpostgres.ResizeOperation) api.ManagedPostgresComputePolicyChange {
	out := api.ManagedPostgresComputePolicyChange{ID: operation.ID, DatabaseID: operation.DatabaseID,
		FromScaleToZero: operation.SourceSpec.ScaleToZero, TargetScaleToZero: operation.TargetScaleToZero,
		Generation: operation.Generation, State: string(operation.State), ConnectionInterruptionExpected: true,
		LastErrorCode: operation.LastErrorCode, CreatedAt: operation.CreatedAt.UTC().Format(time.RFC3339Nano)}
	if !operation.CompletedAt.IsZero() {
		out.CompletedAt = operation.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func (s *server) admitManagedPostgresComputePolicy(ctx context.Context, account state.Account, database string, request api.ChangeManagedPostgresComputePolicyRequest) error {
	if request.ScaleToZero == nil {
		return managedpostgres.ErrInvalid
	}
	// UUID replay remains available after plan or rollout admission changes.
	if _, err := s.managedPostgres.GetComputePolicyChange(ctx, account.ID, database, request.RequestID); err == nil {
		return nil
	} else if !errors.Is(err, managedpostgres.ErrNotFound) {
		return err
	}
	limits, ok := api.ManagedPostgresLimitsFor(account.Plan)
	if !ok || limits.DatabasesMax == 0 || (!*request.ScaleToZero && !limits.AlwaysOnAllowed) {
		return managedpostgres.ErrQuotaExceeded
	}
	return nil
}

func (s *server) changeManagedPostgresComputePolicy(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var request api.ChangeManagedPostgresComputePolicyRequest
	if err := decodeJSON(r, &request); err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	database := r.PathValue("id")
	if err := s.admitManagedPostgresComputePolicy(r.Context(), account, database, request); err != nil {
		managedPostgresProblem(w, err)
		return
	}
	operation, err := s.managedPostgres.ChangeComputePolicy(r.Context(), managedpostgres.ChangeComputePolicyRequest{
		AccountID: account.ID, DatabaseID: database, RequestID: request.RequestID, ScaleToZero: *request.ScaleToZero})
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.database.compute_policy_requested", &account.ID,
		map[string]any{"database_id": database, "request_id": operation.ID, "scale_to_zero": operation.TargetScaleToZero, "generation": operation.Generation})
	w.Header().Set("Location", "/v1/postgres/databases/"+url.PathEscape(database)+"/compute-policy-changes/"+url.PathEscape(operation.ID))
	writeJSON(w, http.StatusAccepted, managedPostgresComputePolicyView(operation))
}

func (s *server) getManagedPostgresComputePolicyChange(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	operation, err := s.managedPostgres.GetComputePolicyChange(r.Context(), account.ID, r.PathValue("id"), r.PathValue("change_id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, managedPostgresComputePolicyView(operation))
}
