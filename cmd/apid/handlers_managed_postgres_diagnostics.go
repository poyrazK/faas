package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getManagedPostgresAccountingDiagnostics(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if allowed, problem := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, problem)
		return
	}
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	accountID, afterID, limit, err := managedPostgresDiagnosticParameters(r)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	target, err := s.store.AccountByID(r.Context(), accountID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", "the target account does not exist"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load target account"))
		return
	}
	page, err := s.managedPostgres.AccountingDiagnostics(r.Context(), target.ID, afterID, limit, time.Now().UTC())
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, managedPostgresAccountingDiagnosticsView(page))
}

func managedPostgresDiagnosticParameters(r *http.Request) (string, string, int, error) {
	account, err := uuid.Parse(strings.TrimSpace(r.PathValue("account_id")))
	if err != nil {
		return "", "", 0, managedpostgres.ErrInvalid
	}
	after := r.URL.Query().Get("after")
	if after != "" {
		cursor, err := uuid.Parse(after)
		if err != nil {
			return "", "", 0, managedpostgres.ErrInvalid
		}
		after = cursor.String()
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			return "", "", 0, managedpostgres.ErrInvalid
		}
	}
	if limit < 1 || limit > 100 {
		return "", "", 0, managedpostgres.ErrInvalid
	}
	return account.String(), after, limit, nil
}

func managedPostgresAccountingDiagnosticsView(page managedpostgres.AccountingDiagnostics) api.ManagedPostgresAccountingDiagnosticsResponse {
	view := api.ManagedPostgresAccountingDiagnosticsResponse{AccountID: page.AccountID, EvaluatedAt: page.EvaluatedAt,
		PolicyEnabled: page.PolicyEnabled, WindowSeconds: int64(page.Window / time.Second), NextCursor: page.NextCursor,
		Items: make([]api.ManagedPostgresAccountingDiagnostic, 0, len(page.Items))}
	for _, d := range page.Items {
		view.Items = append(view.Items, api.ManagedPostgresAccountingDiagnostic{
			DatabaseID: d.DatabaseID, Name: d.Name, State: string(d.State), AccountingRequired: d.AccountingRequired,
			IdentityKnown: d.IdentityKnown, AccountingDatabaseID: d.AccountingDatabaseID, Blocking: d.Blocking, Reasons: d.Reasons,
			RequiredFrom: diagnosticTime(d.RequiredFrom), RequiredUntil: diagnosticTime(d.RequiredUntil),
			CollectedWindowSeconds: int64(d.Progress.Window / time.Second), CollectedFrom: diagnosticTime(d.Progress.CollectedFrom),
			CollectedUntil: diagnosticTime(d.Progress.CollectedUntil), ObservedAt: diagnosticTime(d.Progress.ObservedAt),
			CorrectionObservedAt: diagnosticTime(d.Progress.CorrectionObservedAt), CorrectionRequiredAt: diagnosticTime(d.CorrectionRequiredAt),
			LeaseUntil: diagnosticTime(d.LeaseUntil),
		})
	}
	return view
}

func diagnosticTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}
