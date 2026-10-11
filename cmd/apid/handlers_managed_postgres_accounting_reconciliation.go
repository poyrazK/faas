package main

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewManagedPostgresAccountingReconciliation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.managedPostgresAccountingReconciliation(w, r, acct, false)
}

func (s *server) applyManagedPostgresAccountingReconciliation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.managedPostgresAccountingReconciliation(w, r, acct, true)
}

func (s *server) managedPostgresAccountingReconciliation(w http.ResponseWriter, r *http.Request, acct state.Account, apply bool) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	account, err := uuid.Parse(r.PathValue("account_id"))
	if err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	var request api.ManagedPostgresAccountingReconciliationRequest
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := decodeJSON(r, &request); err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	result, err := s.managedPostgres.ReconcileAccounting(r.Context(), account.String(), acct.ID, managedpostgres.AccountingReconciliationRequest(request), apply)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.ManagedPostgresAccountingReconciliationResult(result))
}
