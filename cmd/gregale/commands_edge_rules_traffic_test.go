// adr: 201
package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildEdgeRuleActionRetryAppliesDefaults(t *testing.T) {
	raw, err := buildEdgeRuleAction("retry", edgeRuleActionInputs{})
	if err != nil {
		t.Fatalf("a bare retry rule must be valid (platform defaults): %v", err)
	}
	var got api.EdgeRuleRetryAction
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.MaxAttempts != api.EdgeRuleRetryDefaultMaxAttempts {
		t.Fatalf("max_attempts = %d, want the default %d", got.MaxAttempts, api.EdgeRuleRetryDefaultMaxAttempts)
	}
	if got.MinRemainingMs != api.EdgeRuleRetryDefaultMinRemainingMs {
		t.Fatalf("min_remaining_ms = %d, want the default %d", got.MinRemainingMs, api.EdgeRuleRetryDefaultMinRemainingMs)
	}
	if got.BudgetPercent != api.EdgeRuleRetryDefaultBudgetPercent || got.BudgetMinRetries != api.EdgeRuleRetryDefaultBudgetMin {
		t.Fatalf("aggregate budget = %d%%/%d, want defaults %d%%/%d", got.BudgetPercent, got.BudgetMinRetries, api.EdgeRuleRetryDefaultBudgetPercent, api.EdgeRuleRetryDefaultBudgetMin)
	}
	if got.AllowNonIdempotent {
		t.Fatal("allow_non_idempotent defaulted to true; replaying POST must be an explicit opt-in")
	}
}

func TestBuildEdgeRuleActionRetryCarriesFlags(t *testing.T) {
	raw, err := buildEdgeRuleAction("retry", edgeRuleActionInputs{
		RetryMaxAttempts:        3,
		RetryAllowNonIdempotent: true,
		RetryMinRemainingMs:     1000,
		RetryBackoffMs:          250,
		RetryBudgetPercent:      25,
		RetryBudgetMinRetries:   4,
	})
	if err != nil {
		t.Fatalf("buildEdgeRuleAction: %v", err)
	}
	var got api.EdgeRuleRetryAction
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.MaxAttempts != 3 || !got.AllowNonIdempotent || got.MinRemainingMs != 1000 || got.BackoffMs != 250 || got.BudgetPercent != 25 || got.BudgetMinRetries != 4 {
		t.Fatalf("action = %+v, want the flag values carried verbatim", got)
	}
}

// The local validator must reject the same shapes the server would, so a
// typo fails before the round-trip.
func TestBuildEdgeRuleActionRetryRejectsBadValues(t *testing.T) {
	cases := []struct {
		name string
		in   edgeRuleActionInputs
		want string
	}{
		{"one attempt is a no-op", edgeRuleActionInputs{RetryMaxAttempts: 1}, "max_attempts"},
		{"above the ceiling", edgeRuleActionInputs{RetryMaxAttempts: 99}, "max_attempts"},
		{"backoff above cap", edgeRuleActionInputs{RetryBackoffMs: 99999}, "backoff_ms"},
		{"min_remaining above cap", edgeRuleActionInputs{RetryMinRemainingMs: 99999999}, "min_remaining_ms"},
		{"budget percent above cap", edgeRuleActionInputs{RetryBudgetPercent: 101}, "budget_percent"},
		{"budget minimum above cap", edgeRuleActionInputs{RetryBudgetMinRetries: 33}, "budget_min_retries"},
	}
	for _, tc := range cases {
		_, err := buildEdgeRuleAction("retry", tc.in)
		if err == nil {
			t.Fatalf("%s: accepted, want a local validation error", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want it to name %q", tc.name, err, tc.want)
		}
	}
}

func TestBuildEdgeRuleActionCircuitBreakerAppliesDefaults(t *testing.T) {
	raw, err := buildEdgeRuleAction("circuit_breaker", edgeRuleActionInputs{})
	if err != nil {
		t.Fatalf("a bare circuit_breaker rule must be valid: %v", err)
	}
	var got api.EdgeRuleCircuitBreakerAction
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.FailureThreshold != api.EdgeRuleCircuitDefaultFailureThreshold {
		t.Fatalf("failure_threshold = %g, want the default", got.FailureThreshold)
	}
	if got.MinRequests != api.EdgeRuleCircuitDefaultMinRequests {
		t.Fatalf("min_requests = %d, want the default", got.MinRequests)
	}
	if got.MaxOpenSeconds < got.OpenSeconds {
		t.Fatalf("max_open_seconds %d < open_seconds %d after defaulting", got.MaxOpenSeconds, got.OpenSeconds)
	}
}

func TestBuildEdgeRuleActionCircuitBreakerRejectsBadValues(t *testing.T) {
	cases := []struct {
		name string
		in   edgeRuleActionInputs
		want string
	}{
		{"threshold is a ratio", edgeRuleActionInputs{CircuitFailureThreshold: 5}, "failure_threshold"},
		{"window above cap", edgeRuleActionInputs{CircuitWindowSeconds: 99999}, "window_seconds"},
		{"ceiling below interval", edgeRuleActionInputs{CircuitOpenSeconds: 60, CircuitMaxOpenSeconds: 5}, "max_open_seconds"},
	}
	for _, tc := range cases {
		_, err := buildEdgeRuleAction("circuit_breaker", tc.in)
		if err == nil {
			t.Fatalf("%s: accepted, want a local validation error", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want it to name %q", tc.name, err, tc.want)
		}
	}
}

// The CLI vocabulary and the server's closed set must agree, or a kind the
// server accepts is unreachable from the CLI (or vice versa).
func TestEdgeRuleKindVocabCoversTrafficPrimitives(t *testing.T) {
	want := map[string]bool{"retry": false, "circuit_breaker": false}
	for _, k := range edgeRuleKindVocab {
		if _, ok := want[k]; ok {
			want[k] = true
		}
	}
	for kind, found := range want {
		if !found {
			t.Fatalf("edgeRuleKindVocab is missing %q; the CLI would reject a kind the server accepts", kind)
		}
	}
}
