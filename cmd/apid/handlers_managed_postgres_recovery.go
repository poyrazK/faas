package main

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getManagedPostgresRecoveryStatus(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	if r.URL.RawQuery != "" {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	status, err := s.managedPostgres.GetRecoveryStatus(r.Context(), account.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, managedPostgresRecoveryView(status))
}

func managedPostgresRecoveryView(status managedpostgres.RecoveryStatus) api.ManagedPostgresRecoveryStatus {
	out := api.ManagedPostgresRecoveryStatus{DatabaseID: status.DatabaseID, Status: status.Status,
		Fresh: status.Fresh, HistoryBoundsKnown: status.HistoryBoundsKnown,
		RetentionSeconds: status.RetentionSeconds, LastErrorCode: status.LastErrorCode}
	if !status.CheckedAt.IsZero() {
		out.CheckedAt = status.CheckedAt.UTC().Format(time.RFC3339Nano)
	}
	if !status.EarliestPossibleTime.IsZero() {
		out.EarliestPossibleTime = status.EarliestPossibleTime.UTC().Format(time.RFC3339Nano)
	}
	if !status.LatestPossibleTime.IsZero() {
		out.LatestPossibleTime = status.LatestPossibleTime.UTC().Format(time.RFC3339Nano)
	}
	return out
}
