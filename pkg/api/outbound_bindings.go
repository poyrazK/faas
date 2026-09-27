package api

import (
	"math"
	"time"
)

// OutboundIntegrationOffer is an account-visible managed integration. It
// intentionally contains no provider credential or gateway admission token;
// its request limits are effective for customer integrations after applying
// the account plan ceiling.
type OutboundIntegrationOffer struct {
	ID                   string                `json:"id"`
	Name                 string                `json:"name"`
	Origin               string                `json:"origin"`
	AllowedMethods       []string              `json:"allowed_methods"`
	AllowedPathPrefixes  []string              `json:"allowed_path_prefixes"`
	Enabled              bool                  `json:"enabled"`
	CredentialSource     string                `json:"credential_source"`
	CredentialConfigured bool                  `json:"credential_configured"`
	OwnerKind            string                `json:"owner_kind"`
	DailyRequestLimit    *int64                `json:"daily_request_limit"`
	RequestPolicy        OutboundRequestPolicy `json:"request_policy"`
}

// OutboundRequestPolicy is the per-integration admission and timeout policy.
// Customer-owned values are effective after applying the account plan ceiling.
type OutboundRequestPolicy struct {
	RatePerSecond    float64 `json:"rate_per_second"`
	Burst            int     `json:"burst"`
	MaxInFlight      int     `json:"max_in_flight"`
	RequestTimeoutMS int     `json:"request_timeout_ms"`
}

// DefaultOutboundRequestPolicy preserves the original customer-integration
// admission defaults.
func DefaultOutboundRequestPolicy() OutboundRequestPolicy {
	limits := MustLimitsFor(PlanFree)
	return OutboundRequestPolicy{
		RatePerSecond:    limits.OutboundRatePerSecondMax,
		Burst:            limits.OutboundBurstMax,
		MaxInFlight:      limits.OutboundMaxInFlightMax,
		RequestTimeoutMS: limits.OutboundRequestTimeoutMSMax,
	}
}

// EffectiveOutboundRequestPolicyForPlan applies the current plan's safety
// ceilings to a persisted policy. A plan downgrade therefore takes effect on
// the next outbound admission without rewriting the configured policy.
func EffectiveOutboundRequestPolicyForPlan(plan Plan, policy OutboundRequestPolicy) (OutboundRequestPolicy, bool) {
	limits, ok := LimitsFor(plan)
	if !ok || limits.OutboundRatePerSecondMax <= 0 || limits.OutboundBurstMax < 1 ||
		limits.OutboundMaxInFlightMax < 1 || limits.OutboundRequestTimeoutMSMax < 1 ||
		policy.RatePerSecond <= 0 || math.IsNaN(policy.RatePerSecond) || math.IsInf(policy.RatePerSecond, 0) ||
		policy.Burst < 1 || policy.MaxInFlight < 1 || policy.RequestTimeoutMS < 1 {
		return OutboundRequestPolicy{}, false
	}
	if policy.RatePerSecond > limits.OutboundRatePerSecondMax {
		policy.RatePerSecond = limits.OutboundRatePerSecondMax
	}
	if policy.Burst > limits.OutboundBurstMax {
		policy.Burst = limits.OutboundBurstMax
	}
	if policy.MaxInFlight > limits.OutboundMaxInFlightMax {
		policy.MaxInFlight = limits.OutboundMaxInFlightMax
	}
	if policy.RequestTimeoutMS > limits.OutboundRequestTimeoutMSMax {
		policy.RequestTimeoutMS = limits.OutboundRequestTimeoutMSMax
	}
	return policy, true
}

// OutboundRequestPolicyAllowedForPlan reports whether a customer-selected
// policy is valid without silently clamping an API write.
func OutboundRequestPolicyAllowedForPlan(plan Plan, policy OutboundRequestPolicy) bool {
	effective, ok := EffectiveOutboundRequestPolicyForPlan(plan, policy)
	return ok && effective == policy
}

// CreateOutboundIntegrationRequest creates a customer-owned managed
// integration. Its provider Authorization value is uploaded separately.
type CreateOutboundIntegrationRequest struct {
	Name                string                 `json:"name"`
	Origin              string                 `json:"origin"`
	AllowedMethods      []string               `json:"allowed_methods"`
	AllowedPathPrefixes []string               `json:"allowed_path_prefixes"`
	DailyRequestLimit   *int64                 `json:"daily_request_limit,omitempty"`
	RequestPolicy       *OutboundRequestPolicy `json:"request_policy,omitempty"`
}

// PutOutboundRequestPolicyRequest replaces all four admission policy values.
type PutOutboundRequestPolicyRequest struct {
	RequestPolicy OutboundRequestPolicy `json:"request_policy"`
}

// PutOutboundCredentialRequest sets or rotates the provider Authorization
// value. The response intentionally has no equivalent secret field.
type PutOutboundCredentialRequest struct {
	Authorization string `json:"authorization"`
}

// PutOutboundDailyRequestBudgetRequest sets a per-integration daily limit;
// null removes the customer-selected limit.
type PutOutboundDailyRequestBudgetRequest struct {
	DailyRequestLimit *int64 `json:"daily_request_limit"`
}

// OutboundIntegrationUsageResponse reports UTC-day gateway admissions. Calls
// count when admitted, including those whose upstream request later fails.
// DailyRequestLimit is the effective optional cap after applying the account
// plan ceiling.
type OutboundIntegrationUsageResponse struct {
	DailyRequestCount int64     `json:"daily_request_count"`
	DailyRequestLimit *int64    `json:"daily_request_limit"`
	UsageDate         string    `json:"usage_date"`
	ResetsAt          time.Time `json:"resets_at"`
}

type OutboundIntegrationOfferList struct {
	Items []OutboundIntegrationOffer `json:"items"`
}

type OutboundAppBinding struct {
	Integration         OutboundIntegrationOffer `json:"integration"`
	AppID               string                   `json:"app_id"`
	AllowedMethods      []string                 `json:"allowed_methods"`
	AllowedPathPrefixes []string                 `json:"allowed_path_prefixes"`
	CreatedAt           time.Time                `json:"created_at"`
}

// UpdateOutboundBindingPolicyRequest narrows an app's route access within
// the integration policy. It cannot change the provider origin or widen it.
type UpdateOutboundBindingPolicyRequest struct {
	AllowedMethods      []string `json:"allowed_methods"`
	AllowedPathPrefixes []string `json:"allowed_path_prefixes"`
}

type OutboundAppBindingList struct {
	Items []OutboundAppBinding `json:"items"`
}
