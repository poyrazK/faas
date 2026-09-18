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
	if got := effectiveInvocationRetryPolicy(app, nil); string(got) != string(app.RetryPolicyJSON) {
		t.Fatalf("inherited policy = %s, want %s", got, app.RetryPolicyJSON)
	}
	override := &api.RetryPolicyDTO{MaxAttempts: 2, BaseSeconds: 1}
	got := effectiveInvocationRetryPolicy(app, override)
	var policy api.RetryPolicyDTO
	if err := json.Unmarshal(got, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.MaxAttempts != 2 || policy.BaseSeconds != 1 {
		t.Fatalf("override policy = %+v, want max_attempts=2 base_seconds=1", policy)
	}
}

func TestMarshalAppRetryPolicyRejectsInvalidValues(t *testing.T) {
	if _, problem := marshalAppRetryPolicy(&api.RetryPolicyDTO{BaseSeconds: -1}); problem == nil {
		t.Fatal("expected invalid retry policy problem")
	}
}
