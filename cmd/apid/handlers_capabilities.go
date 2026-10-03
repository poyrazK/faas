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
func (s *server) getCapabilities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	capabilities, err := api.CapabilitiesForPlan(acct.Plan)
	if err != nil {
		logCustomerFailure(s.log, "resolve customer capabilities", err)
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Available features temporarily unavailable",
			"Gregale could not load the available features for this account.").
			WithHeader("Retry-After", "5").
			WithHint("Retry in a moment; if it continues, contact support."))
		return
	}
	// Entitlement is only one half of availability. A capability must also be
	// backed by a runtime that is enabled and configured on this control plane;
	// otherwise clients would advertise a feature whose first request returns
	// 501/503.
	for i := range capabilities.Capabilities {
		available := true
		switch capabilities.Capabilities[i].Key {
		case "openapi-contract-preview":
			available = api.ApiContractDiffEnabled()
		case disposableRunsCapabilityKey:
			available = s.executionAPIEnabled
		case "object-storage":
			available = s.objectStorageProvisioningReady()
		case "github-deploys":
			available = s.githubDeploysAvailable != nil && s.githubDeploysAvailable(r.Context())
		}
		applyCapabilityRuntimeAvailability(&capabilities.Capabilities[i], available)
	}
	writeJSON(w, http.StatusOK, capabilities)
}

// Plan restrictions take precedence when both gates deny access. Runtime
// readiness describes the existing installation gate, not a health guarantee.
func applyCapabilityRuntimeAvailability(capability *api.CapabilityStatus, available bool) {
	if !capability.Enabled || available {
		return
	}
	capability.Enabled = false
	capability.UnavailableReason = api.CapabilityUnavailableRuntime
	capability.UnavailableDetail = "This feature is not available on this Gregale installation right now. Contact support for availability."
}
