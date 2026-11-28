package outbound

import (
	"testing"
	"time"
)

// Prod hunt 2026-11-28: a call naming no max_tokens at 9% of a daily cost
// cap was refused as "budget is exhausted" with Retry-After until midnight.
// A spent budget, a call whose own reservation does not fit, and a budget
// held by calls in flight are now told apart.
func TestBudgetRefusalKinds(t *testing.T) {
	now := time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)
	limit := int64Ref(100)
	for _, tc := range []struct {
		name       string
		state      aiBudgetState
		reserve    int64
		reason     string
		shortRetry bool
	}{
		{"spent", aiBudgetState{dailyCost: 100}, 1, ReasonDailyCostLimit, false},
		{"own reservation too large", aiBudgetState{dailyCost: 9}, 92, ReasonDailyCostLimit + ReasonReservationSuffix, false},
		{"held by calls in flight", aiBudgetState{dailyCost: 9, inflightCost: 80}, 20, ReasonDailyCostLimit, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason, retry, limited := aiBudgetExhausted(now, tc.state, aiBudgetLimits{dailyCost: limit}, aiReservation{cost: tc.reserve})
			if !limited || reason != tc.reason {
				t.Fatalf("= %q, %v; want %q", reason, limited, tc.reason)
			}
			if tc.shortRetry != (retry == aiBudgetHeldRetry) {
				t.Fatalf("retry = %s", retry)
			}
		})
	}
	if _, _, limited := aiBudgetExhausted(now, aiBudgetState{dailyCost: 9}, aiBudgetLimits{dailyCost: limit}, aiReservation{cost: 91}); limited {
		t.Fatal("a call that fits exactly was refused")
	}
	for reason, want := range map[string]string{
		ReasonDailyCostLimit:                           "outbound_budget_exhausted",
		ReasonDailyCostLimit + ReasonReservationSuffix: "outbound_ai_reservation_exceeds_budget",
		ReasonRequestsPerMinute:                        "outbound_rate_limited",
		ReasonConcurrency:                              "outbound_rate_limited",
		ReasonProviderCooldown:                         "outbound_provider_cooldown",
		ReasonTraceCallLimit:                           "outbound_budget_exhausted",
	} {
		if code, _ := admissionRefusalProblem(reason); code != want {
			t.Errorf("%s = %s, want %s", reason, code, want)
		}
	}
}
