// adr: 201
package api_test

// Per-plan matrices for the ADR-201 traffic-resilience quotas. Mirrors the
// shape of TestEdgeRulesCachePerApp_* so the quota ladder for every edge-rule
// kind is pinned the same way.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

var trafficPrimitiveLadder = []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale}

func TestEdgeRulesRetryPerApp_PerPlanMatrix(t *testing.T) {
	want := map[api.Plan]int{
		api.PlanFree:  0,
		api.PlanHobby: 3,
		api.PlanPro:   10,
		api.PlanScale: 25,
	}
	for plan, expected := range want {
		l := api.MustLimitsFor(plan)
		if l.EdgeRulesRetryPerApp != expected {
			t.Errorf("%s: EdgeRulesRetryPerApp = %d, want %d", plan, l.EdgeRulesRetryPerApp, expected)
		}
		if l.EdgeRulesRetryPerApp > l.EdgeRulesPerApp {
			t.Errorf("%s: EdgeRulesRetryPerApp (%d) > EdgeRulesPerApp (%d); a per-kind cap cannot exceed the per-app total",
				plan, l.EdgeRulesRetryPerApp, l.EdgeRulesPerApp)
		}
	}
}

// Free is 0 for a capacity reason rather than a packaging one, and the
// reasoning is worth pinning: a Free app is capped at max_concurrency 1, so a
// replay has no healthy sibling to land on and would only spend the admission
// ledger twice for the same request.
func TestEdgeRulesRetryPerApp_FreeZeroBecauseNoSiblingExists(t *testing.T) {
	l := api.MustLimitsFor(api.PlanFree)
	if l.EdgeRulesRetryPerApp != 0 {
		t.Errorf("PlanFree.EdgeRulesRetryPerApp = %d, want 0", l.EdgeRulesRetryPerApp)
	}
	if l.MaxConcurrency > 1 {
		t.Fatalf("PlanFree.MaxConcurrency = %d; if Free ever admits a second instance, "+
			"revisit EdgeRulesRetryPerApp — the Free=0 rationale is that no sibling exists to retry against",
			l.MaxConcurrency)
	}
}

func TestEdgeRulesCircuitBreakerPerApp_PerPlanMatrix(t *testing.T) {
	want := map[api.Plan]int{
		api.PlanFree:  0,
		api.PlanHobby: 3,
		api.PlanPro:   10,
		api.PlanScale: 25,
	}
	for plan, expected := range want {
		l := api.MustLimitsFor(plan)
		if l.EdgeRulesCircuitBreakerPerApp != expected {
			t.Errorf("%s: EdgeRulesCircuitBreakerPerApp = %d, want %d",
				plan, l.EdgeRulesCircuitBreakerPerApp, expected)
		}
		if l.EdgeRulesCircuitBreakerPerApp > l.EdgeRulesPerApp {
			t.Errorf("%s: EdgeRulesCircuitBreakerPerApp (%d) > EdgeRulesPerApp (%d)",
				plan, l.EdgeRulesCircuitBreakerPerApp, l.EdgeRulesPerApp)
		}
	}
}

func TestEgressCircuitBreakersPerApp_PerPlanMatrix(t *testing.T) {
	want := map[api.Plan]int{
		api.PlanFree:  0,
		api.PlanHobby: 3,
		api.PlanPro:   10,
		api.PlanScale: 50,
	}
	for plan, expected := range want {
		l := api.MustLimitsFor(plan)
		if l.EgressCircuitBreakersPerApp != expected {
			t.Errorf("%s: EgressCircuitBreakersPerApp = %d, want %d",
				plan, l.EgressCircuitBreakersPerApp, expected)
		}
		// An egress breaker can only exist for an upstream the ADR-098
		// capture path already recorded, so the breaker quota may never
		// exceed the hint quota that produces its subjects.
		if l.EgressCircuitBreakersPerApp > l.DataPlacementHintsPerApp {
			t.Errorf("%s: EgressCircuitBreakersPerApp (%d) > DataPlacementHintsPerApp (%d); "+
				"a breaker needs a captured upstream to attach to",
				plan, l.EgressCircuitBreakersPerApp, l.DataPlacementHintsPerApp)
		}
	}
}

func TestTrafficPrimitiveQuotas_MonotonicLadder(t *testing.T) {
	for i := 1; i < len(trafficPrimitiveLadder); i++ {
		prev := api.MustLimitsFor(trafficPrimitiveLadder[i-1])
		curr := api.MustLimitsFor(trafficPrimitiveLadder[i])
		if curr.EdgeRulesRetryPerApp < prev.EdgeRulesRetryPerApp {
			t.Errorf("%s.EdgeRulesRetryPerApp (%d) < %s (%d)",
				trafficPrimitiveLadder[i], curr.EdgeRulesRetryPerApp,
				trafficPrimitiveLadder[i-1], prev.EdgeRulesRetryPerApp)
		}
		if curr.EdgeRulesCircuitBreakerPerApp < prev.EdgeRulesCircuitBreakerPerApp {
			t.Errorf("%s.EdgeRulesCircuitBreakerPerApp (%d) < %s (%d)",
				trafficPrimitiveLadder[i], curr.EdgeRulesCircuitBreakerPerApp,
				trafficPrimitiveLadder[i-1], prev.EdgeRulesCircuitBreakerPerApp)
		}
		if curr.EgressCircuitBreakersPerApp < prev.EgressCircuitBreakersPerApp {
			t.Errorf("%s.EgressCircuitBreakersPerApp (%d) < %s (%d)",
				trafficPrimitiveLadder[i], curr.EgressCircuitBreakersPerApp,
				trafficPrimitiveLadder[i-1], prev.EgressCircuitBreakersPerApp)
		}
	}
}
