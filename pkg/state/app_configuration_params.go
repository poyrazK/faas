package state

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"net/netip"
	"time"
)

// applyAppConfigurationParams is shared by legacy app edits and environment
// revisions. Ownership, quotas and lifecycle side effects stay in the stores.
func applyAppConfigurationParams(a App, p UpdateAppParams) App {
	if p.SetMaintenanceMode {
		a.MaintenanceMode = boolOrFalse(p.MaintenanceMode)
	}
	if p.SetAppProtocol {
		a.AppProtocol = derefString(p.AppProtocol)
	}
	if p.RAMMB != nil {
		a.RAMMB = *p.RAMMB
	}
	if p.CPUMillicores != nil {
		a.CPUMillicores = *p.CPUMillicores
	}
	if p.SetIdleTimeout {
		a.IdleTimeoutS = intOrZero(p.IdleTimeoutS)
	}
	if p.MaxConcurrency != nil {
		a.MaxConcurrency = *p.MaxConcurrency
	}
	if p.Manifest != nil {
		a.Manifest = *p.Manifest
	}
	if p.SetMinInstances {
		a.MinInstances = intOrZero(p.MinInstances)
	}
	if p.SetEgressAllowlist {
		// ADR-031 + ADR-032: nil-with-Set is treated as "clear to
		// default (empty)" so the API can express "drop the
		// allowlist back to no-list" via a PATCH with
		// egress_allowlist:[] ; non-nil is copied verbatim. The
		// slice is intentionally reallocated so the caller can't
		// mutate the stored value through the slice header it holds
		// after the call returns. v4 + v6 entries share the same
		// counter (Pro 16, Scale 64) — see
		// pkg/api/limits.go::EgressAllowlistMaxSize.
		src := derefPrefixes(p.EgressAllowlist)
		dst := make([]netip.Prefix, len(src))
		copy(dst, src)
		a.EgressAllowlist = dst
	}
	// ADR-118: per-app ingress IP allowlist. Same Set-bit convention
	// as EgressAllowlist above. The DB trigger at
	// migrations/00308_apps_public_auth_ip_allowlist.sql rejects
	// non-v4/v6 families and masklen /0 (defence in depth on top of
	// the apid parse step); the in-memory store trusts the apid
	// layer to have already validated. Plan-gated upstream — Free/Hobby
	// never reach this branch (apid returns 403 before the store
	// is touched).
	if p.SetPublicAuthIPAllowlist {
		src := derefPrefixes(p.PublicAuthIPAllowlist)
		dst := make([]netip.Prefix, len(src))
		copy(dst, src)
		a.PublicAuthIPAllowlist = dst
	}
	// Issue #169 / #172: per-app reactive scale-up trigger. Set
	// distinguishes "unset" (don't touch) from "explicit zero"
	// (disable). Apid already gated the plan and the bounds
	// (RPS > 0, CPU in [1,100]); the store is a plain column write.
	if p.SetAutoscaleTargetRPS {
		a.AutoscaleTargetRPS = intOrZero(p.AutoscaleTargetRPS)
	}
	if p.SetAutoscaleTargetCPUPct {
		a.AutoscaleTargetCPUPct = intOrZero(p.AutoscaleTargetCPUPct)
	}
	// Issue #471: per-app streaming flag. Same Set-bit convention as
	// the autoscale targets — SetStreamingEnabled distinguishes "don't
	// touch" from "explicit false" (opt out of streaming). Apid
	// already gated the plan; the store is a plain column write.
	if p.SetStreamingEnabled {
		a.StreamingEnabled = boolOrFalse(p.StreamingEnabled)
	}
	// Issue #676 / ADR-080: per-app raw-bytes Upgrade bridge. Same
	// Set-bit convention as streaming_enabled above — the Set bit
	// distinguishes "don't touch" from "explicit false" (opt out
	// of Upgrade traffic). Apid already gated the plan; the store
	// is a plain column write.
	if p.SetWebSocketEnabled {
		a.WebSocketEnabled = boolOrFalse(p.WebSocketEnabled)
	}
	// ADR-093: per-route observability opt-in. Same Set-bit
	// convention as WebSocketEnabled above — the Set bit
	// distinguishes "don't touch" from "explicit false" (opt out
	// of per-route metrics). Apid already gated the plan; the
	// store is a plain column write.
	if p.SetRouteMetricsEnabled {
		a.RouteMetricsEnabled = boolOrFalse(p.RouteMetricsEnabled)
	}
	if p.SetOnlyAllowDeclaredRoutes {
		a.OnlyAllowDeclaredRoutes = boolOrFalse(p.OnlyAllowDeclaredRoutes)
	}
	if p.SetDeclaredRoutes {
		src := derefDeclaredRoutes(p.DeclaredRoutes)
		a.DeclaredRoutes = append([]DeclaredRoute(nil), src...)
		for i := range a.DeclaredRoutes {
			a.DeclaredRoutes[i].Methods = append([]string(nil), a.DeclaredRoutes[i].Methods...)
		}
	}
	// Issue #462 / ADR-058 / PR-A: per-app scaling policy. The
	// Set bit is the canonical "unset vs explicit zero" signal;
	// when Set is true the jsonb column is overwritten (deep-copied
	// to avoid caller-mutation aliasing) and the legacy
	// `min_instances` column is kept in sync so the reaper + SDK
	// see the same floor. The policy is the canonical source at
	// PR-A; the legacy column is the projection.
	if p.SetScalingPolicy && p.ScalingPolicy != nil {
		copyPolicy := *p.ScalingPolicy
		a.ScalingPolicy = &copyPolicy
		// Sync the legacy min_instances column with the policy's
		// MinInstances. Mirrors the pgstore's policy-comes-first
		// CASE so the in-memory and on-disk shapes stay
		// consistent.
		if p.ScalingPolicy.MinInstances != 0 {
			a.MinInstances = p.ScalingPolicy.MinInstances
		}
	}
	if p.SetRetryPolicy {
		if p.RetryPolicyJSON == nil || len(*p.RetryPolicyJSON) == 0 {
			a.RetryPolicyJSON = json.RawMessage(`{}`)
		} else {
			a.RetryPolicyJSON = append(json.RawMessage(nil), (*p.RetryPolicyJSON)...)
		}
	}
	// Issue #472 / ADR-054: per-app cosign signature-enforcement flag.
	// Same Set-bit convention — SetRequireSigned distinguishes "don't
	// touch" from "explicit false" (opt out of signed-image enforcement).
	// Apid already gated the admin scope; the store is a plain column
	// write. imaged reads this at buildImageLayer time.
	if p.SetRequireSigned {
		a.RequireSigned = boolOrFalse(p.RequireSigned)
	}
	if p.SetSecurityPolicy && p.SecurityPolicy != nil && p.SecurityPolicy.Valid() {
		a.SecurityPolicy = *p.SecurityPolicy
	}
	// Issue #470 / ADR-055: per-app warm-snapshot knobs. Same Set-bit
	// convention as require_signed / streaming_enabled — the Set bit
	// distinguishes "don't touch" from "explicit reset". Apid already
	// gated the plan (Free/Hobby + true is rejected) and the bounds
	// (1..100 / 100..60000); the store is a plain column write.
	if p.SetWarmSnapshotEnabled {
		a.WarmSnapshotEnabled = boolOrFalse(p.WarmSnapshotEnabled)
	}
	if p.SetWarmSnapshotMinRequests {
		a.WarmSnapshotMinRequests = intOrZero(p.WarmSnapshotMinRequests)
	}
	if p.SetWarmSnapshotMinMs {
		a.WarmSnapshotMinMs = intOrZero(p.WarmSnapshotMinMs)
	}
	// Issue #1056 / ADR-074: desired paused warm-pool size. The API
	// layer owns plan and max-concurrency validation; MemStore mirrors
	// the durable Set-bit semantics for tests and local development.
	if p.SetWarmPoolSize {
		a.WarmPoolSize = intOrZero(p.WarmPoolSize)
	}
	// Issue #475: eviction_priority ('best_effort'|'reserved') follows
	// the same Set*/optional-pointer pattern as warm_snapshot_*. The
	// plan gate (Plan.EvictionPriorityReservedAllowed) and the per-account
	// cap (Plan.ReservedConcurrencyPerAccount) are enforced upstream in
	// apid; the store is a plain column write. derefString coerces a
	// nil pointer to "" which is harmless because the CASE guard in
	// UpdateApp's SQL short-circuits the read on !SetEvictionPriority.
	if p.SetEvictionPriority {
		a.EvictionPriority = derefString(p.EvictionPriority)
	}
	// Issue #560: per-app require_authn opt-in. Same Set-bit
	// convention as require_signed / streaming_enabled — the Set
	// bit distinguishes "don't touch" from "explicit false"
	// (opt out — back to public-by-default). Apid already gated
	// the plan (Free/Hobby + true is rejected with 403
	// plan_require_authn_not_allowed); the store is a plain
	// column write. The memstore mirrors the pgstore shape so
	// every test that exercises UpdateApp sees the same
	// behaviour regardless of backend.
	if p.SetRequireAuthn {
		a.RequireAuthn = boolOrFalse(p.RequireAuthn)
	}
	if p.SetConsumerAuthMode && p.ConsumerAuthMode != nil {
		a.ConsumerAuthMode = ConsumerAuthMode(*p.ConsumerAuthMode)
	}
	if p.SetPlatformTenantRequired {
		a.PlatformTenantRequired = boolOrFalse(p.PlatformTenantRequired)
	}
	if p.SetVisibility && p.Visibility != nil {
		a.Visibility = api.NormalizeAppVisibility(*p.Visibility)
	}
	// Issue #477 / ADR-079: per-app public_auth
	// (open|bearer|basic). Memstore mirrors the on-disk shape —
	// PublicAuthMode is the column-equivalent text + the
	// PublicAuthBasicSealed byte slice is the secretbox blob
	// (encrypted by the apid seal step before persistence).
	// A PATCH mode='open' or mode='bearer' clears the
	// sealed blob so a stale secretbox row from a previous
	// mode='basic' PATCH never reaches a fresh request.
	// SetPublicAuth distinguishes "unset" from
	// "explicit set"; when Set is true the store overwrites
	// the column-shaped fields verbatim.
	if p.SetPublicAuth && p.PublicAuth != nil {
		a.PublicAuthMode = p.PublicAuth.Mode
		a.PublicAuthBasicSealed = append([]byte(nil), p.PublicAuth.Sealed...)
	}
	// Phase 5 repo decomposition (ADR-050 §3): pkg/reconcile uses
	// these to stamp a fresh workload identity on a changed app. The
	// apid handler never sets them (customers don't touch root_dir
	// / workload_name / start_command via PATCH today). nil = leave
	// alone; non-nil pointer = copy verbatim.
	if p.RootDir != nil {
		a.RootDir = *p.RootDir
	}
	if p.WorkloadName != nil {
		a.WorkloadName = *p.WorkloadName
	}
	if p.WorkloadClass != nil {
		a.WorkloadClass = *p.WorkloadClass
	}
	if p.StartCommand != nil {
		a.StartCommand = *p.StartCommand
	}
	// Issue #695 / ADR-080: grand-father clear path. Mirrors the
	// pgstore $44 CASE so the in-memory and on-disk shapes stay
	// consistent. Apid sets ClearAuthDefaultFlippedAt whenever the
	// customer made a deliberate PATCH choice on require_authn OR
	// public_auth; the stamp clears and the dashboard banner count
	// drops. No-op for new post-flip apps (column is already NULL)
	// and for no-touch PATCHes (SetRequireAuthn/SetPublicAuth false).
	if p.ClearAuthDefaultFlippedAt {
		a.AuthDefaultFlippedAt = nil
	}
	// Tier A10 / ADR-088: per-app overflow_node preference.
	// Set bit controls the write — "don't touch" by default,
	// "explicit NULL" (clear → A9 fallback) when Set is true
	// with a nil pointer, and "set" when Set is true with a
	// non-nil pointer. Apid has already validated the UUID
	// against the empty-uuid CHECK + FK with ON DELETE SET
	// NULL (migration 00167) before reaching this path; the
	// store is a plain column write. Memstore mirrors the
	// pgstore shape so every test that exercises UpdateApp
	// sees the same behaviour regardless of backend.
	if p.SetOverflowNode {
		if p.OverflowNode == nil {
			a.OverflowNode = nil
		} else {
			s := *p.OverflowNode
			a.OverflowNode = &s
		}
	}
	// CORS improvements D1: per-app default CORS opt-in +
	// allowlist. Same Set-bit convention as the other partial
	// PATCH fields. SetCORSDefaultEnabled distinguishes "don't
	// touch" from "explicit false" (opt out of an enabled
	// fallback). SetCORSDefaultOrigins distinguishes "don't
	// touch" from "explicit empty list" (clear an enabled
	// fallback). The validator runs above the store layer so
	// we never see (enabled=true, origins=nil) here.
	if p.SetCORSDefaultEnabled {
		// Lifts nullable wire shape into the
		// store-layer pointer field. nil → nil
		// (legacy row, opt-out triple collapse),
		// *v → *v (no copy needed; the apid
		// caller already handed off ownership).
		a.CORSDefaultEnabled = p.CORSDefaultEnabled
	}
	if p.SetCORSDefaultOrigins {
		if p.CORSDefaultOrigins == nil {
			a.CORSDefaultOrigins = nil
		} else {
			src := *p.CORSDefaultOrigins
			dst := make([]string, len(src))
			copy(dst, src)
			a.CORSDefaultOrigins = dst
		}
	}
	if p.SetRequestRateLimitRPS {
		a.RequestRateLimitRPS = positiveIntPointer(p.RequestRateLimitRPS)
	}
	if p.SetRequestRateLimitBurst {
		a.RequestRateLimitBurst = positiveIntPointer(p.RequestRateLimitBurst)
	}
	// ADR-361: extra egress ports; empty clears, mirroring PgStore's
	// nil-for-empty scan.
	if p.SetEgressPorts {
		a.EgressPorts = nil
		if len(p.EgressPorts) > 0 {
			a.EgressPorts = append([]int(nil), p.EgressPorts...)
		}
	}
	// ADR-119: per-app static egress IP. SetStaticEgressIP
	// distinguishes "don't touch" (false) from "explicit set or
	// clear" (true). Apid gates the plan and the IPv4-only
	// shape; the store is a plain column write. nil pointer with
	// Set=true means "clear" (DELETE wire shape), copying the
	// pgstore's CASE-based $57+$58 shape so an in-memory test
	// sees the same persistence surface as a real DB call.
	if p.SetStaticEgressIP {
		if p.StaticEgressIP == nil {
			a.StaticEgressIP = nil
			a.StaticEgressIPSetAt = nil
		} else {
			cp := *p.StaticEgressIP
			a.StaticEgressIP = &cp
			now := time.Now().UTC()
			a.StaticEgressIPSetAt = &now
		}
	}
	return a
}
