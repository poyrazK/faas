package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const disposableRunsCapabilityKey = "disposable-runs"

// getCapabilities returns the canonical customer capability registry with
// plan entitlement resolved for the authenticated account. It is read-only
// and intentionally does not require MFA so API keys can use it during setup.
func (s *server) getCapabilities(w http.ResponseWriter, _ *http.Request, acct state.Account) {
	capabilities, err := api.CapabilitiesForPlan(acct.Plan)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Capability registry unavailable", err.Error()))
		return
	}
	// Plan entitlement alone is not enough to advertise disposable runs. The
	// execution handlers use this same boot-time gate and return 501 while the
	// scheduler/VM isolation path is unavailable. Keep the capability registry
	// fail-closed so clients do not try an API that this host cannot serve.
	for i := range capabilities.Capabilities {
		if capabilities.Capabilities[i].Key == disposableRunsCapabilityKey {
			capabilities.Capabilities[i].Enabled = capabilities.Capabilities[i].Enabled && s.executionAPIEnabled
			break
		}
	}
	writeJSON(w, http.StatusOK, capabilities)
}
