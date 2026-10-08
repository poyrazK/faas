// adr: 570
package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTrafficPreviewAgreesWithStoredRetryCompiler(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action state.EdgeRuleRetryAction
	}{
		{"stored_zeros", state.EdgeRuleRetryAction{MaxAttempts: 2}},
		{"configured", state.EdgeRuleRetryAction{MaxAttempts: 3, AllowNonIdempotent: true, MinRemainingMs: 350, BackoffMs: 80, BudgetPercent: 15, BudgetMinRetries: 2}},
		{"out_of_range", state.EdgeRuleRetryAction{MaxAttempts: 99, MinRemainingMs: -1, BackoffMs: 99999, BudgetPercent: 101, BudgetMinRetries: 999}},
		{"zero_attempts", state.EdgeRuleRetryAction{}},
		{"one_attempt", state.EdgeRuleRetryAction{MaxAttempts: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := retryRule("retry", &tc.action)
			compiled, errs := compileRetryRules([]state.EdgeRule{stored})
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			raw, err := json.Marshal(stored.Action)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := edgeruletrace.Simulate(edgeruletrace.Input{App: "demo", Host: stored.MatchHost, Path: "/", Method: http.MethodGet, AppMaintenanceLoaded: true}, []api.EdgeRuleResponse{{
				ID: stored.ID, Kind: "retry", MatchHost: stored.MatchHost, Enabled: true, Action: raw,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if len(compiled) == 0 {
				if preview.Simulation.Outcome != "continue" || len(preview.Simulation.Steps) != 0 || preview.Rules[0].Outcome != "unavailable" {
					t.Fatalf("dropped rule intercepted preview: %+v", preview)
				}
				return
			}
			policy := preview.Simulation.Steps[0].RetryPolicy
			actual := compiled[0].Policy()
			minimum := actual.BudgetMinRetries
			if minimum <= 0 {
				minimum = api.EdgeRuleRetryDefaultBudgetMin
			}
			if policy == nil || policy.MaxAttempts != actual.MaxAttempts || policy.AllowNonIdempotent != actual.AllowNonIdempotent || int64(policy.MinRemainingMS) != actual.MinRemaining.Milliseconds() || int64(policy.BackoffMS) != actual.Backoff.Milliseconds() || policy.BudgetPercent != actual.BudgetPercent || policy.BudgetMinRetries != minimum {
				t.Fatalf("preview=%+v compiled runtime=%+v", policy, actual)
			}
		})
	}
}

func TestTrafficPreviewAgreesWithStoredBudgetCompiler(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action state.EdgeRuleBudgetAction
	}{
		{"configured", state.EdgeRuleBudgetAction{BudgetMs: 1200, TotalDeadlineMs: 4000}},
		{"execution_clamp", state.EdgeRuleBudgetAction{BudgetMs: -1, TotalDeadlineMs: 4000}},
		{"execution_only", state.EdgeRuleBudgetAction{BudgetMs: 1200}},
		{"invalid_total", state.EdgeRuleBudgetAction{BudgetMs: 1200, TotalDeadlineMs: -1}},
		{"total_above_platform", state.EdgeRuleBudgetAction{BudgetMs: 1200, TotalDeadlineMs: int(api.RequestBudgetMax.Milliseconds()) + 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := state.EdgeRule{ID: "budget", AccountID: "acct-1", AppID: "app-1", MatchHost: "example.com", Enabled: true,
				Kind: state.EdgeRuleKindBudget, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindBudget, Budget: &tc.action}}
			compiled, failures := compileBudgetRules([]state.EdgeRule{stored})
			entry := &gateway.HostEntry{Budget: compiled, PathGlobErrs: failures, PolicyRuleOwners: map[string]string{stored.ID: stored.AccountID}}
			if err := entry.SealPolicy(); err != nil {
				t.Fatal(err)
			}
			ctx, err := gateway.WithPinnedHostPolicy(t.Context(), stored.MatchHost, entry)
			if err != nil {
				t.Fatal(err)
			}
			verificationErr := gateway.ValidatePinnedHostPolicies(ctx, stored.AccountID)
			raw, err := json.Marshal(stored.Action)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := edgeruletrace.Simulate(edgeruletrace.Input{App: "demo", Host: stored.MatchHost, Path: "/", Method: http.MethodGet,
				AppMaintenanceLoaded: true, AppRequestBudgetLoaded: true, RequestBudgetMS: 3000, RequestBudgetMaxMS: api.RequestBudgetMax.Milliseconds()},
				[]api.EdgeRuleResponse{{ID: stored.ID, Kind: "budget", MatchHost: stored.MatchHost, Enabled: true, Action: raw}})
			if err != nil {
				t.Fatal(err)
			}
			step := preview.Simulation.Steps[0]
			if len(compiled) == 0 {
				if verificationErr == nil || step.RuleID != stored.ID || preview.Simulation.ProblemCode != api.CodeTrafficPolicyUnavailable || preview.Simulation.StatusCode != http.StatusServiceUnavailable || preview.Simulation.TotalDeadlinePolicy != nil {
					t.Fatalf("invalid total bypassed snapshot verification: %+v runtime error=%v", preview.Simulation, verificationErr)
				}
				return
			}
			if verificationErr != nil {
				t.Fatal(verificationErr)
			}
			if step.RuleID != "budget" || step.BudgetPolicy.BudgetMS != int64(compiled[0].BudgetMs) || step.BudgetPolicy.TotalDeadlineMS != int64(compiled[0].TotalDeadlineMs) {
				t.Fatalf("preview=%+v compiled=%+v", step, compiled[0])
			}
		})
	}
}
