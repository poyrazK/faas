package api

import "testing"

func TestNormalizeServiceReliabilityPolicies(t *testing.T) {
	bindings := ServiceBindingsForTargets([]string{"billing"})
	got, err := NormalizeServiceReliabilityPolicies(map[string]ServiceReliabilityPolicy{
		" Billing ": {TimeoutMS: 1200, MaxAttempts: 1},
	}, bindings)
	if err != nil || got["billing"].TimeoutMS != 1200 || got["billing"].MaxAttempts != 1 {
		t.Fatalf("normalized policies = %#v, err=%v", got, err)
	}
	for _, tc := range []struct {
		name string
		mapIn map[string]ServiceReliabilityPolicy
	}{
		{"undeclared", map[string]ServiceReliabilityPolicy{"database": {TimeoutMS: 100}}},
		{"duplicate", map[string]ServiceReliabilityPolicy{"billing": {}, "BILLING": {}}},
		{"long timeout", map[string]ServiceReliabilityPolicy{"billing": {TimeoutMS: MaxServiceReliabilityTimeoutMS + 1}}},
		{"bad attempts", map[string]ServiceReliabilityPolicy{"billing": {MaxAttempts: EdgeRuleRetryMaxAttempts + 1}}},
		{"impossible replay floor", map[string]ServiceReliabilityPolicy{"billing": {TimeoutMS: 100, MinRemainingMS: 200}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NormalizeServiceReliabilityPolicies(tc.mapIn, bindings); err == nil {
				t.Fatal("invalid dependency policy accepted")
			}
		})
	}
}
