// adr: 201
package main

// Compile-step tests for the ADR-201 kinds. apid-Validate already bounds
// these fields, so everything here is about the defence-in-depth pass: a row
// that reached Postgres another way (direct write, seedEdgeRuleDirect) must
// not produce an unsafe compiled rule.

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func retryRule(id string, action *state.EdgeRuleRetryAction) state.EdgeRule {
	return state.EdgeRule{
		ID: id, AccountID: "acct-1", AppID: "app-1",
		MatchHost: "h.example", Enabled: true,
		Kind:   state.EdgeRuleKindRetry,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRetry, Retry: action},
	}
}

func breakerRule(id string, action *state.EdgeRuleCircuitBreakerAction) state.EdgeRule {
	return state.EdgeRule{
		ID: id, AccountID: "acct-1", AppID: "app-1",
		MatchHost: "h.example", Enabled: true,
		Kind:   state.EdgeRuleKindCircuitBreaker,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCircuitBreaker, CircuitBreaker: action},
	}
}

func TestCompileRetryRulesCarriesEffectiveValues(t *testing.T) {
	out, errs := compileRetryRules([]state.EdgeRule{
		retryRule("r1", &state.EdgeRuleRetryAction{
			MaxAttempts: 2, AllowNonIdempotent: true, MinRemainingMs: 500, BackoffMs: 100,
		}),
	})
	if len(errs) != 0 {
		t.Fatalf("parse errors = %v, want none", errs)
	}
	if len(out) != 1 {
		t.Fatalf("compiled = %d rules, want 1", len(out))
	}
	got := out[0]
	if got.MaxAttempts != 2 || !got.AllowNonIdempotent {
		t.Fatalf("compiled = %+v, want the stored attempts and opt-in", got)
	}
	if got.MinRemaining != 500*time.Millisecond || got.Backoff != 100*time.Millisecond {
		t.Fatalf("compiled durations = %v/%v, want 500ms/100ms", got.MinRemaining, got.Backoff)
	}
	if policy := got.Policy(); !policy.Enabled || policy.MaxAttempts != 2 {
		t.Fatalf("Policy() = %+v, want an enabled 2-attempt policy", policy)
	}
}

// A rule that cannot replay must be DROPPED, not lifted to the default.
// Clamping upward would silently enable retry on a route whose stored row
// says otherwise, which is the wrong direction for a primitive that can
// double a customer's side effect.
func TestCompileRetryRulesDropsNonReplayableRatherThanClamping(t *testing.T) {
	for _, attempts := range []int{0, 1, -5} {
		out, _ := compileRetryRules([]state.EdgeRule{
			retryRule("r1", &state.EdgeRuleRetryAction{MaxAttempts: attempts}),
		})
		if len(out) != 0 {
			t.Fatalf("max_attempts=%d compiled to %+v; a rule that cannot replay must be dropped, never lifted to the default",
				attempts, out)
		}
	}
}

func TestCompileRetryRulesClampsAttemptsToCeiling(t *testing.T) {
	out, _ := compileRetryRules([]state.EdgeRule{
		retryRule("r1", &state.EdgeRuleRetryAction{MaxAttempts: 99}),
	})
	if len(out) != 1 {
		t.Fatalf("compiled = %d rules, want 1", len(out))
	}
	if out[0].MaxAttempts != api.EdgeRuleRetryMaxAttempts {
		t.Fatalf("max_attempts = %d, want the platform ceiling %d",
			out[0].MaxAttempts, api.EdgeRuleRetryMaxAttempts)
	}
}

func TestCompileRetryRulesFallsBackOnOutOfRangeBudgetFields(t *testing.T) {
	out, _ := compileRetryRules([]state.EdgeRule{
		retryRule("r1", &state.EdgeRuleRetryAction{
			MaxAttempts: 2, MinRemainingMs: -1, BackoffMs: 999999,
		}),
	})
	if len(out) != 1 {
		t.Fatalf("compiled = %d rules, want 1", len(out))
	}
	want := time.Duration(api.EdgeRuleRetryDefaultMinRemainingMs) * time.Millisecond
	if out[0].MinRemaining != want {
		t.Fatalf("min_remaining = %v, want the default %v", out[0].MinRemaining, want)
	}
	if out[0].Backoff != 0 {
		t.Fatalf("backoff = %v, want 0 for an out-of-range stored value", out[0].Backoff)
	}
}

func TestCompileRetryRulesSkipsDisabledAndForeignKinds(t *testing.T) {
	disabled := retryRule("r1", &state.EdgeRuleRetryAction{MaxAttempts: 2})
	disabled.Enabled = false
	missingAction := retryRule("r2", nil)
	foreign := state.EdgeRule{
		ID: "r3", AppID: "app-1", Enabled: true, Kind: state.EdgeRuleKindCache,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCache},
	}
	out, _ := compileRetryRules([]state.EdgeRule{disabled, missingAction, foreign})
	if len(out) != 0 {
		t.Fatalf("compiled = %+v, want nothing from a disabled rule, a nil action, or another kind", out)
	}
}

func TestCompileCircuitBreakerRulesCarriesEffectiveValues(t *testing.T) {
	out, errs := compileCircuitBreakerRules([]state.EdgeRule{
		breakerRule("cb1", &state.EdgeRuleCircuitBreakerAction{
			FailureThreshold: 0.25, MinRequests: 20,
			WindowSeconds: 30, OpenSeconds: 10, MaxOpenSeconds: 120,
		}),
	})
	if len(errs) != 0 || len(out) != 1 {
		t.Fatalf("compiled = %d rules, errs = %v; want 1 and none", len(out), errs)
	}
	got := out[0]
	if got.FailureThreshold != 0.25 || got.MinRequests != 20 {
		t.Fatalf("compiled = %+v, want the stored threshold and min_requests", got)
	}
	if got.Window != 30*time.Second || got.OpenDuration != 10*time.Second || got.MaxOpenDuration != 120*time.Second {
		t.Fatalf("compiled durations = %v/%v/%v, want 30s/10s/120s", got.Window, got.OpenDuration, got.MaxOpenDuration)
	}
}

// max_open below open would make the breaker ill-formed. Lifting the ceiling
// to `open` keeps protection the customer asked for (with no growth) rather
// than dropping the rule.
func TestCompileCircuitBreakerLiftsCeilingBelowOpenInterval(t *testing.T) {
	out, _ := compileCircuitBreakerRules([]state.EdgeRule{
		breakerRule("cb1", &state.EdgeRuleCircuitBreakerAction{
			FailureThreshold: 0.5, MinRequests: 5,
			WindowSeconds: 10, OpenSeconds: 30, MaxOpenSeconds: 5,
		}),
	})
	if len(out) != 1 {
		t.Fatalf("compiled = %d rules, want the rule kept", len(out))
	}
	if out[0].MaxOpenDuration != out[0].OpenDuration {
		t.Fatalf("max_open = %v, open = %v; the ceiling must be lifted to the interval, never left below it",
			out[0].MaxOpenDuration, out[0].OpenDuration)
	}
}

func TestCompileCircuitBreakerDefaultsOutOfRangeFields(t *testing.T) {
	out, _ := compileCircuitBreakerRules([]state.EdgeRule{
		breakerRule("cb1", &state.EdgeRuleCircuitBreakerAction{
			FailureThreshold: 7, MinRequests: -3, WindowSeconds: 0, OpenSeconds: 99999,
		}),
	})
	if len(out) != 1 {
		t.Fatalf("compiled = %d rules, want 1", len(out))
	}
	got := out[0]
	if got.FailureThreshold != api.EdgeRuleCircuitDefaultFailureThreshold {
		t.Fatalf("failure_threshold = %g, want the default", got.FailureThreshold)
	}
	if got.MinRequests != api.EdgeRuleCircuitDefaultMinRequests {
		t.Fatalf("min_requests = %d, want the default", got.MinRequests)
	}
	if got.Window != time.Duration(api.EdgeRuleCircuitDefaultWindowSeconds)*time.Second {
		t.Fatalf("window = %v, want the default", got.Window)
	}
	if got.OpenDuration != time.Duration(api.EdgeRuleCircuitDefaultOpenSeconds)*time.Second {
		t.Fatalf("open = %v, want the default", got.OpenDuration)
	}
}

// Priority ordering is what makes PickFirstXMatch correct, so it is the
// compile step's job, not the matcher's.
func TestCompileTrafficRulesSortByPriority(t *testing.T) {
	low := retryRule("low", &state.EdgeRuleRetryAction{MaxAttempts: 2})
	low.Priority = 10
	high := retryRule("high", &state.EdgeRuleRetryAction{MaxAttempts: 2})
	high.Priority = 1
	out, _ := compileRetryRules([]state.EdgeRule{low, high})
	if len(out) != 2 || out[0].ID != "high" {
		t.Fatalf("compiled order = %v, want priority-ascending with \"high\" first", out)
	}

	cbLow := breakerRule("low", &state.EdgeRuleCircuitBreakerAction{MinRequests: 5})
	cbLow.Priority = 10
	cbHigh := breakerRule("high", &state.EdgeRuleCircuitBreakerAction{MinRequests: 5})
	cbHigh.Priority = 1
	cbOut, _ := compileCircuitBreakerRules([]state.EdgeRule{cbLow, cbHigh})
	if len(cbOut) != 2 || cbOut[0].ID != "high" {
		t.Fatalf("compiled order = %v, want priority-ascending", cbOut)
	}
}

// A malformed path glob must drop the rule and surface the parse error, so
// the customer sees a no-match rather than a rule steering traffic randomly.
func TestCompileTrafficRulesReportMalformedGlobs(t *testing.T) {
	bad := retryRule("r1", &state.EdgeRuleRetryAction{MaxAttempts: 2})
	bad.MatchPath = "/v1/[unclosed"
	out, errs := compileRetryRules([]state.EdgeRule{bad})
	if len(out) != 0 {
		t.Fatalf("compiled = %+v, want the malformed rule dropped", out)
	}
	if len(errs) == 0 {
		t.Fatal("no PathGlobError reported; a typo must be operator-visible")
	}
}
