package api

// Egress overage election (ADR-20261104204500). Without it every plan stops
// at its monthly egress allowance; with it, egress above the allowance is
// billed as overage while egress billing is live.

// EgressOverageDocsURL documents the election.
const EgressOverageDocsURL = "https://github.com/poyrazK/faas/blob/main/docs/billing.md#egress-overage"

// CodePlanEgressOverageNotAllowed: the plan cannot elect egress overage
// (Free stops at its allowance; PlanLimits.EgressOverageElectable).
const CodePlanEgressOverageNotAllowed = "plan_egress_overage_not_allowed"

// EgressOverageBilling values: whether egress above the allowance is billed
// while the election is on.
const (
	// EgressOverageBillingLive: the provider bills the plan's egress; an
	// election that is on lifts the ceiling.
	EgressOverageBillingLive = "live"
	// EgressOverageBillingOff: egress is not billed yet; the ceiling holds
	// and a saved election takes effect once billing goes live.
	EgressOverageBillingOff = "off"
)
