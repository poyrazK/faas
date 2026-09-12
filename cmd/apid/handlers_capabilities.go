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
	// Entitlement is only one half of availability. A capability must also be
	// backed by a runtime that is enabled and configured on this control plane;
	// otherwise clients would advertise a feature whose first request returns
	// 501/503.
	for i := range capabilities.Capabilities {
		switch capabilities.Capabilities[i].Key {
		case "openapi-contract-preview":
			capabilities.Capabilities[i].Enabled = capabilities.Capabilities[i].Enabled && api.ApiContractDiffEnabled()
		case "disposable-runs":
			capabilities.Capabilities[i].Enabled = capabilities.Capabilities[i].Enabled && s.executionAPIEnabled
		case "object-storage":
			capabilities.Capabilities[i].Enabled = capabilities.Capabilities[i].Enabled && s.objectStorageEnabled()
		}
	}
	writeJSON(w, http.StatusOK, capabilities)
}
