// adr: 531
package api

import (
	"math"
	"net/http"
	"testing"
)

func TestExecutionBudgetClampsBeforeDurationConversion(t *testing.T) {
	rule := EdgeRuleBudgetAction{BudgetMs: 2400}
	for _, value := range []string{"18446744078709", "9223372036854775807", "6500"} {
		t.Run(value, func(t *testing.T) {
			policy := ResolveRequestExecutionBudget(3000, 5000, 0, &rule,
				http.Header{http.CanonicalHeaderKey(RequestBudgetDefaultOverrideHeader): []string{value}})
			if policy.BudgetMS != 5000 || policy.Source != "ceiling_clamp" || policy.OverrideStatus != "applied_clamped" {
				t.Fatalf("oversized positive override must select the ceiling: %+v", policy)
			}
		})
	}
	policy := ResolveRequestExecutionBudget(3000, 5000, math.MaxInt, nil, nil)
	if policy.ConfiguredMS != math.MaxInt64 || policy.BudgetMS != 5000 || policy.Source != "app" {
		t.Fatalf("app timeout wrapped before clamp: %+v", policy)
	}
}

func TestExecutionBudgetIgnoresInvalidOverrides(t *testing.T) {
	rule := EdgeRuleBudgetAction{BudgetMs: 2400}
	for _, value := range []string{"-1", "0", "3s", "1.5", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			policy := ResolveRequestExecutionBudget(3000, 5000, 0, &rule,
				http.Header{http.CanonicalHeaderKey(RequestBudgetDefaultOverrideHeader): []string{value}})
			if policy.BudgetMS != 2400 || policy.Source != "rule" || policy.OverrideStatus != "ignored_invalid" {
				t.Fatalf("invalid override changed rule budget: %+v", policy)
			}
		})
	}
}

func TestStoredRetryCompilationDoesNotApplyWriteDefaults(t *testing.T) {
	stored := EdgeRuleRetryAction{MaxAttempts: 2}
	compiled, valid := CompileRetryActionForRuntime(stored)
	if !valid || compiled.MinRemainingMs != 0 || compiled.BudgetMinRetries != 0 || compiled.BudgetPercent != EdgeRuleRetryDefaultBudgetPercent {
		t.Fatalf("stored zero floor/minimum changed: %+v valid=%t", compiled, valid)
	}
	written := stored
	if problem := written.Validate(); problem != nil {
		t.Fatal(problem)
	}
	if written.MinRemainingMs != EdgeRuleRetryDefaultMinRemainingMs || written.BudgetMinRetries != EdgeRuleRetryDefaultBudgetMin {
		t.Fatalf("API write defaults changed: %+v", written)
	}
}
