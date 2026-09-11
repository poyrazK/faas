package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getCapabilities returns the canonical customer capability registry with
// plan entitlement resolved for the authenticated account. It is read-only
// and intentionally does not require MFA so API keys can use it during setup.
func (s *server) getCapabilities(w http.ResponseWriter, _ *http.Request, acct state.Account) {
	capabilities, err := api.CapabilitiesForPlan(acct.Plan)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Capability registry unavailable", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, capabilities)
}
