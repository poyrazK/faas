package apidserver

import (
	"net/http"
	"slices"
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
		case "crash-snapshots":
			// Hunt 34 H34-16b: listed as available while FAAS_CRASH_SNAPSHOTS
			// was unset and every request answered 501.
			available = s.crashSnapshotsEnabled
		case "production-forks":
			available = s.appForksEnabled
		case "profile-captures":
			// Hunt 34 H34-16: `debug capture` answered "on-demand profiling
			// is not enabled on this installation" while the list said
			// available.
			available = s.profileCaptures.Enabled
		case "release-commands":
			// imaged fails a release command while FAAS_RELEASE_PHASE_ENABLED
			// is off, so it is advertised only when apid says it is on.
			available = !s.releasePhaseOff
		case "outbound-integrations", "workload-identity":
			// The outbound relay authenticates the instance with a
			// vmmd-signed workload-identity token. ADR-1141 provisioning
			// installs apid's cluster JWKS with vmmd's key; without it every
			// relay call is answered 503, so the feature is not advertised
			// (found in production 2026-11-09). Pull leases are not gated
			// here: they also accept API keys with queues:consume, which work
			// without workload identity (ADR-20261110120000).
			available = s.workloadVerifier != nil
			if flagKey, ok := platformflags.FlagForCapability(capabilities.Capabilities[i].Key); ok {
				available = available && s.platformFlagFor(flagKey, flagSubject(r, acct))
			}
		case "app-tasks", "tcp-listeners", "udp-listeners", dataAPICapability, api.ManagedValkeyCapability:
			// Prod findings 2026-11-15 #25: these were listed as available
			// while their runtime was off and every request was refused.
			available = s.capabilityRuntimeReady(capabilities.Capabilities[i].Key)
			if flagKey, ok := platformflags.FlagForCapability(capabilities.Capabilities[i].Key); ok {
				available = available && s.platformFlagFor(flagKey, flagSubject(r, acct))
			}
		case "realtime", "realtime-access", "realtime-channels", "realtime-connections", "realtime-occupancy",
			"realtime-publishers", "realtime-push", "realtime-tickets", "realtime-workers":
			// The realtime routes decide by realtime node, preview rollout,
			// daemon switch and per-feature flag; the registry used to check
			// only the capability flag, so gregale_context and the CLI
			// offered features whose routes answered 404 (2026-11-22).
			available, _ = s.realtime.CapabilityAvailable(r, acct, capabilities.Capabilities[i].Key)
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
	applyParentRuntimeAvailability(capabilities.Capabilities)
	capabilities.HiddenCapabilities = mergeHiddenCapabilities(s.hiddenCapabilities(flagSubject(r, acct)),
		s.realtime.PreviewHiddenCapabilities(acct.ID))
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

// mergeHiddenCapabilities appends extra to hidden, skipping duplicates, so
// capabilities hidden by a rollout outside the flag gate are hidden too.
func mergeHiddenCapabilities(hidden, extra []string) []string {
	for _, id := range extra {
		if !slices.Contains(hidden, id) {
			hidden = append(hidden, id)
		}
	}
	return hidden
}

// capabilityRuntimeReady reports whether this control plane runs what a
// capability's requests need: the app-task API gate, tcpd/udpd at the edge,
// or the managed PostgreSQL bindings the Data API is set up from
// (configurePipelineDataAPI).
func (s *server) capabilityRuntimeReady(key string) bool {
	switch key {
	case "app-tasks":
		return s.appTaskAPIEnabled
	case "tcp-listeners":
		return s.tcpListenersEnabled
	case "udp-listeners":
		return s.udpListenersEnabled
	case dataAPICapability:
		return s.managedPostgres != nil && s.managedPostgresBindings != nil && s.notif != nil
	case api.ManagedValkeyCapability:
		// Agent eval 2026-11-23: a release converge dropped the private TCP
		// listener, yet managed Redis stayed listed and redis create kept
		// making caches no app could reach.
		return s.privateServiceTCPEnabled()
	}
	return true
}

// applyParentRuntimeAvailability marks a capability unavailable when an
// ancestor in the catalog is unavailable on this installation: bucket CORS,
// versioning, lifecycle and the rest were listed as available while
// object-storage itself was not, and every one of their requests failed
// (prod hunt 2026-11-21). Only the runtime reason is inherited; a child's
// own plan list still decides plan access.
func applyParentRuntimeAvailability(capabilities []api.CapabilityStatus) {
	catalog, err := productcap.Load()
	if err != nil {
		return
	}
	parent := make(map[string]string, len(catalog.Capabilities))
	for _, c := range catalog.Capabilities {
		parent[c.ID] = c.Parent
	}
	runtimeOff := map[string]bool{}
	for _, c := range capabilities {
		if !c.Enabled && c.UnavailableReason == api.CapabilityUnavailableRuntime {
			runtimeOff[c.Key] = true
		}
	}
	for i := range capabilities {
		for id, depth := parent[capabilities[i].Key], 0; id != "" && depth < 8; id, depth = parent[id], depth+1 {
			if runtimeOff[id] {
				applyCapabilityRuntimeAvailability(&capabilities[i], false)
				break
			}
		}
	}
}

// Plan restrictions take precedence when both gates deny access. Runtime
// readiness describes the existing installation gate, not a health guarantee.
func applyCapabilityRuntimeAvailability(capability *api.CapabilityStatus, available bool) {
	if !capability.Enabled || available {
		return
	}
	capability.Enabled = false
	capability.UnavailableReason = api.CapabilityUnavailableRuntime
	capability.UnavailableDetail = runtimeUnavailableDetail(capability.Key)
}

// runtimeUnavailableDetail explains a capability this installation does not
// run. Public TCP and UDP listeners are easy to confuse with private TCP
// between a customer's own apps: an agent read "tcp-listeners unavailable"
// as "private TCP is off", told the user managed Redis could not work and to
// contact support, while managed Redis was available (agent eval 2026-11-24).
func runtimeUnavailableDetail(key string) string {
	switch key {
	case "tcp-listeners":
		return "Public TCP ports at the edge are not enabled on this Gregale installation. " +
			"This does not affect private TCP between your own apps (managed Redis, <name>.svc.gregale addresses); see managed-redis and internal-services. " +
			"Contact support if you need a public port."
	case "udp-listeners":
		return "Public UDP ports at the edge are not enabled on this Gregale installation. " +
			"This does not affect traffic between your own apps. Contact support if you need a public port."
	}
	return "This feature is not available on this Gregale installation right now. Contact support for availability."
}
