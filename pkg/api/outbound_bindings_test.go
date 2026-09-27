package api

import (
	"math"
	"testing"
)

func TestOutboundRequestPolicyPlanCeilings(t *testing.T) {
	cases := []struct {
		plan Plan
		max  OutboundRequestPolicy
	}{
		{PlanFree, OutboundRequestPolicy{RatePerSecond: 10, Burst: 20, MaxInFlight: 10, RequestTimeoutMS: 30_000, MaxRetries: 2, ResponseCacheTTLSeconds: MaxOutboundResponseCacheTTLSeconds, RetryBudgetPerMinute: 60}},
		{PlanHobby, OutboundRequestPolicy{RatePerSecond: 20, Burst: 100, MaxInFlight: 50, RequestTimeoutMS: 60_000, MaxRetries: 2, ResponseCacheTTLSeconds: MaxOutboundResponseCacheTTLSeconds, RetryBudgetPerMinute: 120}},
		{PlanPro, OutboundRequestPolicy{RatePerSecond: 100, Burst: 500, MaxInFlight: 250, RequestTimeoutMS: 120_000, MaxRetries: 2, ResponseCacheTTLSeconds: MaxOutboundResponseCacheTTLSeconds, RetryBudgetPerMinute: 600}},
		{PlanScale, OutboundRequestPolicy{RatePerSecond: 500, Burst: 2000, MaxInFlight: 1000, RequestTimeoutMS: 300_000, MaxRetries: 2, ResponseCacheTTLSeconds: MaxOutboundResponseCacheTTLSeconds, RetryBudgetPerMinute: 3000}},
	}
	defaults := DefaultOutboundRequestPolicy()
	if defaults != (OutboundRequestPolicy{RatePerSecond: 10, Burst: 20, MaxInFlight: 10, RequestTimeoutMS: 30_000}) {
		t.Fatalf("outbound defaults = %+v", defaults)
	}
	for _, tc := range cases {
		t.Run(string(tc.plan), func(t *testing.T) {
			if !OutboundRequestPolicyAllowedForPlan(tc.plan, defaults) {
				t.Fatal("default policy must be valid for every plan")
			}
			if !OutboundRequestPolicyAllowedForPlan(tc.plan, tc.max) {
				t.Fatalf("plan ceiling %+v rejected", tc.max)
			}
			effective, ok := EffectiveOutboundRequestPolicyForPlan(tc.plan, OutboundRequestPolicy{
				RatePerSecond: 1000, Burst: 10_000, MaxInFlight: 10_000, RequestTimeoutMS: 600_000,
				MaxRetries: 20, ResponseCacheTTLSeconds: MaxOutboundResponseCacheTTLSeconds * 2, RetryBudgetPerMinute: MaxOutboundRetryBudgetPerMinute,
			})
			if !ok || effective != tc.max {
				t.Fatalf("effective policy = %+v, %v; want %+v", effective, ok, tc.max)
			}
			invalid := tc.max
			invalid.MaxInFlight++
			if OutboundRequestPolicyAllowedForPlan(tc.plan, invalid) {
				t.Fatalf("over-ceiling policy %+v accepted", invalid)
			}
			invalid = tc.max
			invalid.MaxRetries++
			if OutboundRequestPolicyAllowedForPlan(tc.plan, invalid) {
				t.Fatalf("over-ceiling retry policy %+v accepted", invalid)
			}
			invalid = tc.max
			invalid.ResponseCacheTTLSeconds++
			if OutboundRequestPolicyAllowedForPlan(tc.plan, invalid) {
				t.Fatalf("over-ceiling response-cache policy %+v accepted", invalid)
			}
			invalid = tc.max
			invalid.RetryBudgetPerMinute++
			if OutboundRequestPolicyAllowedForPlan(tc.plan, invalid) {
				t.Fatalf("over-ceiling retry-budget policy %+v accepted", invalid)
			}
		})
	}
	if OutboundRequestPolicyAllowedForPlan(PlanPro, OutboundRequestPolicy{RatePerSecond: math.NaN(), Burst: 1, MaxInFlight: 1, RequestTimeoutMS: 1}) {
		t.Fatal("NaN rate accepted")
	}
	if OutboundRequestPolicyAllowedForPlan(PlanPro, OutboundRequestPolicy{RatePerSecond: 1, Burst: 1, MaxInFlight: 1, RequestTimeoutMS: 1, MaxRetries: -1}) {
		t.Fatal("negative retry count accepted")
	}
	if OutboundRequestPolicyAllowedForPlan(PlanPro, OutboundRequestPolicy{RatePerSecond: 1, Burst: 1, MaxInFlight: 1, RequestTimeoutMS: 1, ResponseCacheTTLSeconds: -1}) {
		t.Fatal("negative response-cache TTL accepted")
	}
	for _, policy := range []OutboundRequestPolicy{
		{RatePerSecond: 1, Burst: 1, MaxInFlight: 1, RequestTimeoutMS: 1, CircuitBreakerFailureThreshold: 1},
		{RatePerSecond: 1, Burst: 1, MaxInFlight: 1, RequestTimeoutMS: 1, CircuitBreakerOpenSeconds: 30},
		{RatePerSecond: 1, Burst: 1, MaxInFlight: 1, RequestTimeoutMS: 1, CircuitBreakerFailureThreshold: MaxOutboundCircuitBreakerFailureThreshold + 1, CircuitBreakerOpenSeconds: 30},
		{RatePerSecond: 1, Burst: 1, MaxInFlight: 1, RequestTimeoutMS: 1, CircuitBreakerFailureThreshold: 3, CircuitBreakerOpenSeconds: MaxOutboundCircuitBreakerOpenSeconds + 1},
	} {
		if OutboundRequestPolicyAllowedForPlan(PlanPro, policy) {
			t.Fatalf("invalid circuit-breaker policy accepted: %+v", policy)
		}
	}
	if !OutboundRequestPolicyAllowedForPlan(PlanPro, OutboundRequestPolicy{
		RatePerSecond: 1, Burst: 1, MaxInFlight: 1, RequestTimeoutMS: 1,
		CircuitBreakerFailureThreshold: 3, CircuitBreakerOpenSeconds: 30,
	}) {
		t.Fatal("valid circuit-breaker policy rejected")
	}
	if _, ok := EffectiveOutboundRequestPolicyForPlan(Plan("unknown"), defaults); ok {
		t.Fatal("unknown plan unexpectedly produced an effective policy")
	}
}
