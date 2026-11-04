package apidserver

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/productcap"
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
	_, capabilities.ConditionalParking = s.store.(state.ConditionalAppParkTransitionStore)
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
			available = s.object.ObjectStorageProvisioningReady()
		case "github-deploys", "pr-previews":
			// PR previews are built from the same GitHub push/PR pipeline.
			available = s.githubDeploysAvailable != nil && s.githubDeploysAvailable(r.Context())
		case "custom-domains":
			// production-us hunt #8: with ADR-520 on-demand TLS off, a
			// customer hostname can never obtain a certificate or reach
			// the Cloudflare-fronted edge, yet the feature was advertised.
			available = api.CustomDomainTLSOnDemand()
		default:
			// ADR-998, ADR-1700: a capability released by a platform flag
			// is available only to the accounts the flag includes.
			if flagKey, ok := platformflags.FlagForCapability(capabilities.Capabilities[i].Key); ok {
				available = s.platformFlagFor(flagKey, flagSubject(r, acct))
			}
		}
		// ADR-20261023115741: with the capability gate installed, every
		// capability's flag also decides availability for this account.
		if s.capGate != nil && !s.capGate.Enabled(capabilities.Capabilities[i].Key, flagSubject(r, acct)) {
			available = false
		}
		applyCapabilityRuntimeAvailability(&capabilities.Capabilities[i], available)
	}
	capabilities.HiddenCapabilities = s.hiddenCapabilities(flagSubject(r, acct))
	writeJSON(w, http.StatusOK, capabilities)
}

// openAPIHidden leaves the operator routes and the capabilities off by
// default (off for an account no rule names) out of the anonymous spec and
// /docs, so they document what every account gets (ADR-20261023115741).
func (s *server) openAPIHidden() (string, func(path string) bool) {
	if s.capGate == nil {
		return "", nil
	}
	catalog, err := productcap.Load()
	if err != nil {
		return "", nil
	}
	var hidden []string
	set := map[string]bool{}
	for _, capability := range catalog.Capabilities {
		if !s.capGate.ReleasedByDefault(capability.ID) {
			hidden = append(hidden, capability.ID)
			set[capability.ID] = true
		}
	}
	gate := s.capGate
	return strings.Join(hidden, ","), func(path string) bool { return gate.SpecHidden(path, set) }
}

// hiddenCapabilities lists every catalog capability whose flag, or an
// ancestor's, is off for the subject, so clients can hide what apid hides.
func (s *server) hiddenCapabilities(subject platformflags.Subject) []string {
	if s.capGate == nil {
		return nil
	}
	catalog, err := productcap.Load()
	if err != nil {
		return nil
	}
	var hidden []string
	for _, capability := range catalog.Capabilities {
		if !s.capGate.Enabled(capability.ID, subject) {
			hidden = append(hidden, capability.ID)
		}
	}
	return hidden
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
