package main

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 134
func TestEffectiveInvocationRetryPolicyPrecedence(t *testing.T) {
	app := state.App{RetryPolicyJSON: json.RawMessage(`{"max_attempts":4,"base_seconds":2}`)}
	got := effectiveInvocationRetryPolicy(app, nil, 3)
	var policy api.RetryPolicyDTO
	if err := json.Unmarshal(got, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.MaxAttempts != 3 || policy.BaseSeconds != 2 {
		t.Fatalf("inherited policy = %+v, want plan-capped max_attempts=3 base_seconds=2", policy)
	}
	override := &api.RetryPolicyDTO{MaxAttempts: 2, BaseSeconds: 1}
	got = effectiveInvocationRetryPolicy(app, override, 3)
	if err := json.Unmarshal(got, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.MaxAttempts != 2 || policy.BaseSeconds != 1 {
		t.Fatalf("override policy = %+v, want max_attempts=2 base_seconds=1", policy)
	}
}

func TestEffectiveInvocationRetryPolicyMaterializesNoRetryPlan(t *testing.T) {
	got := effectiveInvocationRetryPolicy(state.App{}, nil, 0)
	var policy api.RetryPolicyDTO
	if err := json.Unmarshal(got, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.MaxAttempts != 1 {
		t.Fatalf("max_attempts = %d, want one delivery attempt and no replay", policy.MaxAttempts)
	}
}

func TestMarshalAppRetryPolicyRejectsInvalidValues(t *testing.T) {
	if _, problem := marshalAppRetryPolicy(&api.RetryPolicyDTO{BaseSeconds: -1}); problem == nil {
		t.Fatal("expected invalid retry policy problem")
	}
}
