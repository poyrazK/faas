// adr: 570
package api

import (
	"math"
	"net/http"
	"testing"
)

func TestExecutionBudgetClampsBeforeDurationConversion(t *testing.T) {
	// Naming the header opts the rule in to raising overrides.
	rule := EdgeRuleBudgetAction{BudgetMs: 2400, AllowOverrideHeader: RequestBudgetDefaultOverrideHeader}
	// The header accepts at most MaxInt32 (#4968); anything larger is invalid below.
	for _, value := range []string{"2147483647", "6500"} {
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

// Any caller can send the default header, so without allow_override_header it
// may only shorten the rule's budget (ADR-093 amendment 2026-11-01).
func TestExecutionBudgetDefaultHeaderOnlyShortensTheRule(t *testing.T) {
	header := func(value string) http.Header {
		return http.Header{http.CanonicalHeaderKey(RequestBudgetDefaultOverrideHeader): []string{value}}
	}
	for _, tc := range []struct {
		name      string
		rule      EdgeRuleBudgetAction
		value     string
		budget    int64
		source    string
		overrides string
	}{
		{"default header shortens", EdgeRuleBudgetAction{BudgetMs: 2400}, "1000", 1000, "header_override", "applied"},
		{"default header cannot raise", EdgeRuleBudgetAction{BudgetMs: 2400}, "4000", 2400, "rule", "ignored_raise"},
		{"named header may raise", EdgeRuleBudgetAction{BudgetMs: 2400, AllowOverrideHeader: RequestBudgetDefaultOverrideHeader}, "4000", 4000, "header_override", "applied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := ResolveRequestExecutionBudget(3000, 5000, 0, &tc.rule, header(tc.value))
			if policy.BudgetMS != tc.budget || policy.Source != tc.source || policy.OverrideStatus != tc.overrides {
				t.Fatalf("override %s on %+v = %+v", tc.value, tc.rule, policy)
			}
		})
	}
}

func TestExecutionBudgetIgnoresInvalidOverrides(t *testing.T) {
	rule := EdgeRuleBudgetAction{BudgetMs: 2400}
	for _, value := range []string{"-1", "0", "3s", "1.5", "2147483648", "18446744078709", "9223372036854775807", "9223372036854775808"} {
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
