package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/dataapievents"
)

// reportDataAPIChangeEvents is the private change hooks route
// (ADR-20261022121642); workload identity only.
func (s *server) reportDataAPIChangeEvents(w http.ResponseWriter, r *http.Request) {
	dataapievents.Handler{Verifier: s.workloadVerifier, Store: s.store, Publisher: &s.appEvents}.ServeHTTP(w, r)
}

// reportDataAPIChangeStatus is the private change hook delivery report route
// (ADR-20261022121642 amendment 1); workload identity only.
func (s *server) reportDataAPIChangeStatus(w http.ResponseWriter, r *http.Request) {
	handler := dataapievents.StatusHandler{Verifier: s.workloadVerifier, Store: s.store}
	if s.dataAPIUsage != nil {
		handler.Reports = s.dataAPIUsage
	}
	handler.ServeHTTP(w, r)
}
