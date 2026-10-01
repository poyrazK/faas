package api

import "time"

// EffectiveAppRequestRateLimits applies optional app-level request bucket
// overrides while enforcing the plan ceiling again at the edge. Zero means
// that dimension inherits its plan default; invalid direct-store values are
// clamped defensively so a malformed row cannot raise the platform ceiling.
func EffectiveAppRequestRateLimits(plan Plan, rpsOverride, burstOverride int) (rps, burst int) {
	limits, ok := LimitsFor(plan)
	if !ok {
		return 0, 0
	}
	rps, burst = limits.RateLimitRPS, limits.RateLimitBurst
	if rpsOverride > 0 {
		rps = rpsOverride
	}
	if burstOverride > 0 {
		burst = burstOverride
	}
	if rps > limits.RateLimitRPS {
		rps = limits.RateLimitRPS
	}
	if burst > limits.RateLimitBurst {
		burst = limits.RateLimitBurst
	}
	return rps, burst
}

// RuntimePolicyStatusResponse reports whether gateway, scheduler, and live VM
// consumers have observed or applied the policy components represented here,
// including gateway request-envelope changes and durable response-cache purges.
// The legacy top-level revision/state/counts describe gateway app-cache and
// traffic policy; each named component has a scoped desired revision. The
// request-policy component filters app-row changes from that combined cursor.
// CPU-limit status reports the mutable host cgroup ceiling; guest RAM and vCPU
// topology remain boot-time configuration.
type RuntimePolicyStatusResponse struct {
	AppID            string                       `json:"app_id"`
	DesiredRevision  int64                        `json:"desired_revision"`
	State            string                       `json:"state"` // active, pending, or unverified
	Coverage         []string                     `json:"coverage"`
	ServingGateways  int                          `json:"serving_gateways"`
	AppliedGateways  int                          `json:"applied_gateways"`
	PendingGateways  int                          `json:"pending_gateways"`
	StaleGateways    int                          `json:"stale_gateways"`
	RequestPolicy    RuntimePolicyComponentStatus `json:"request_policy"`
	EdgeRules        RuntimePolicyComponentStatus `json:"edge_rules"`
	CorsPresets      RuntimePolicyComponentStatus `json:"cors_presets"`
	ResponseCache    RuntimePolicyComponentStatus `json:"response_cache"`
	EgressAllowlist  RuntimePolicyNodeStatus      `json:"egress_allowlist"`
	CPULimit         RuntimePolicyNodeStatus      `json:"cpu_limit"`
	SchedulerScaling RuntimePolicySchedulerStatus `json:"scheduler_scaling"`
	TrafficRuntime   TrafficRuntimeStatus         `json:"traffic_runtime"`
}

// TrafficRuntimeStatus reports fresh daemon wiring observations independently
// of policy convergence. It never attests successful request enforcement.
type TrafficRuntimeStatus struct {
	Scope              string                      `json:"scope"`
	State              string                      `json:"state"`              // observed, partial, or unverified
	EnforcementStatus  string                      `json:"enforcement_status"` // unverified
	ServingGateways    int                         `json:"serving_gateways"`
	FreshGateways      int                         `json:"fresh_gateways"`
	StaleGateways      int                         `json:"stale_gateways"`
	MissingGateways    int                         `json:"missing_gateways"`
	PublicRetry        TrafficRuntimeFeatureStatus `json:"public_retry"`
	RateCounter        TrafficRuntimeFeatureStatus `json:"rate_counter"`
	RetryCounter       TrafficRuntimeFeatureStatus `json:"retry_counter"`
	DeadlineSigning    TrafficRuntimeFeatureStatus `json:"deadline_signing"`
	PolicySnapshot     TrafficRuntimeFeatureStatus `json:"policy_snapshot"`
	SecurityRevocation TrafficRuntimeFeatureStatus `json:"security_revocation"`
	ManagedHTTP        TrafficRuntimeFeatureStatus `json:"managed_http"`
	ManagedCircuit     TrafficRuntimeFeatureStatus `json:"managed_circuit"`
}

// Mode describes wiring: enabled/disabled, central/local, shared/redis/local,
// mixed, or unwired. Missing/stale fleet members make the state unverified.
// Retry counter endpoint disagreement is mixed even when backend types agree.
type TrafficRuntimeFeatureStatus struct {
	State string `json:"state"` // observed, mixed, or unverified
	Mode  string `json:"mode"`
}

// RuntimePolicyComponentStatus reports convergence for a policy component's
// desired revision and fresh consumer observations. A desired revision may be
// an independent ledger position or a scoped projection over a shared ledger.
type RuntimePolicyComponentStatus struct {
	Scope           string `json:"scope,omitempty"` // app or account
	DesiredRevision int64  `json:"desired_revision"`
	State           string `json:"state"` // active, pending, or unverified
	ServingGateways int    `json:"serving_gateways"`
	AppliedGateways int    `json:"applied_gateways"`
	PendingGateways int    `json:"pending_gateways"`
	StaleGateways   int    `json:"stale_gateways"`
}

// RuntimePolicyNodeStatus reports app-scoped egress policy convergence across
// compute nodes that currently host a live instance of the app.
type RuntimePolicyNodeStatus struct {
	Scope           string `json:"scope,omitempty"` // app
	DesiredRevision int64  `json:"desired_revision"`
	State           string `json:"state"` // active, pending, or unverified
	ServingNodes    int    `json:"serving_nodes"`
	AppliedNodes    int    `json:"applied_nodes"`
	PendingNodes    int    `json:"pending_nodes"`
	StaleNodes      int    `json:"stale_nodes"`
}

// RuntimePolicySchedulerStatus reports whether the owning scheduler has
// recently loaded the desired scaling configuration. Active describes policy
// observation, not whether a metric-driven replica target has been reached.
type RuntimePolicySchedulerStatus struct {
	Scope            string     `json:"scope,omitempty"` // app
	DesiredRevision  int64      `json:"desired_revision"`
	ObservedRevision int64      `json:"observed_revision"`
	State            string     `json:"state"` // active, pending, or unverified
	SchedulerNodeID  string     `json:"scheduler_node_id,omitempty"`
	ObservedAt       *time.Time `json:"observed_at,omitempty"`
	Stale            bool       `json:"stale"`
}
