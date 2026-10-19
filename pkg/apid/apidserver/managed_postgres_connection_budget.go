package apidserver

import (
	"net/http"

	postgresapi "github.com/onebox-faas/faas/pkg/apid/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) managedPostgresConnectionBudget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	postgresapi.ConnectionBudgetHandler{Service: s.managedPostgresBindings, Problem: managedPostgresProblem}.Database(w, r, acct)
}

func (s *server) appDataAPIConnectionBudget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	postgresapi.ConnectionBudgetHandler{Service: s.managedPostgresBindings, Problem: managedPostgresProblem}.App(w, r, acct, app)
}

func (s *server) reportDataAPIRuntimeConnections(w http.ResponseWriter, r *http.Request) {
	var service postgresapi.RuntimeConnectionRecorder
	if s.managedPostgresBindings != nil {
		service = s.managedPostgresBindings
	}
	postgresapi.RuntimeConnectionHandler{Service: service, Verifier: s.workloadVerifier, Problem: managedPostgresProblem}.Report(w, r)
}
