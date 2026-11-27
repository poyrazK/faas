package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) checkManagedPostgresMigration(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var source api.ManagedPostgresImportSource
	if err := decodeJSONSized(r, &source, 8<<10); err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	result, err := s.managedPostgres.CheckCustomerMigration(r.Context(), account.ID, r.PathValue("id"), source)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) startManagedPostgresMigration(w http.ResponseWriter, r *http.Request, account state.Account) {
	s.controlManagedPostgresMigration(w, r, account, "start")
}

func (s *server) stopManagedPostgresMigration(w http.ResponseWriter, r *http.Request, account state.Account) {
	s.controlManagedPostgresMigration(w, r, account, "stop")
}

func (s *server) prepareManagedPostgresMigration(w http.ResponseWriter, r *http.Request, account state.Account) {
	s.controlManagedPostgresMigration(w, r, account, "prepare")
}

func (s *server) controlManagedPostgresMigration(w http.ResponseWriter, r *http.Request, account state.Account, action string) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var request api.ManagedPostgresImportRequest
	if err := decodeJSONSized(r, &request, 8<<10); err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	var result api.ManagedPostgresMigrationProgress
	var err error
	if action == "prepare" {
		result, err = s.managedPostgres.PrepareCustomerMigration(r.Context(), account.ID, r.PathValue("id"), request)
	} else {
		result, err = s.managedPostgres.LiveCustomerMigration(r.Context(), account.ID, r.PathValue("id"), request, action == "stop")
	}
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) verifyManagedPostgresMigration(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var request api.ManagedPostgresMigrationVerifyRequest
	if err := decodeJSONSized(r, &request, 8<<10); err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	result, err := s.managedPostgres.VerifyCustomerMigration(r.Context(), account.ID, r.PathValue("id"), request)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) getManagedPostgresMigration(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	result, err := s.managedPostgres.CustomerMigrationStatus(r.Context(), account.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
