package api

import "time"

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

// EgressOverageRequest is PUT /v1/account/egress-overage and
// PUT /v1/orgs/{slug}/egress-overage.
type EgressOverageRequest struct {
	// Enabled turns the election on or off. Required.
	Enabled *bool `json:"enabled"`
}

// EgressOverageResponse is the billing subject's egress overage election.
type EgressOverageResponse struct {
	// Enabled is whether the election is on.
	Enabled bool `json:"enabled"`
	// Electable is whether the plan allows the election.
	Electable bool `json:"electable"`
	// EnabledAt is when the election started; it is kept when the election
	// is turned off and on again.
	EnabledAt *time.Time `json:"enabled_at,omitempty"`
	// DisabledAt is when the election was last turned off.
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
	// IncludedGiB is the plan's monthly egress allowance.
	IncludedGiB int64 `json:"included_gib"`
	// Billing is EgressOverageBillingLive or EgressOverageBillingOff.
	Billing string `json:"billing"`
	// PriceMillicentsPerGiB prices egress above the allowance while billing
	// is live.
	PriceMillicentsPerGiB int64 `json:"price_millicents_per_gib,omitempty"`
	// Currency is the price's currency.
	Currency string `json:"currency,omitempty"`
}
