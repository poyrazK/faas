package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) checkManagedPostgresImport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.managedPostgresImport(w, r, acct, true)
}

func (s *server) startManagedPostgresImport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.managedPostgresImport(w, r, acct, false)
}

func (s *server) managedPostgresImport(w http.ResponseWriter, r *http.Request, acct state.Account, checkOnly bool) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var request api.ManagedPostgresImportRequest
	var err error
	if checkOnly {
		err = decodeJSONSized(r, &request.Source, 8<<10)
	} else {
		err = decodeJSONSized(r, &request, 8<<10)
	}
	if err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	result, err := s.managedPostgres.StartCustomerImport(r.Context(), acct.ID, r.PathValue("id"), request, checkOnly)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	code := http.StatusOK
	if result.State == string(managedpostgres.ImportRunning) {
		code = http.StatusAccepted
	}
	writeJSON(w, code, result)
}

func (s *server) getManagedPostgresImport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	result, err := s.managedPostgres.CustomerImportStatus(r.Context(), acct.ID, r.PathValue("id"), r.PathValue("operation_id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
