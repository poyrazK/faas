package edgeruletrace

import "github.com/onebox-faas/faas/pkg/api"

// ConfiguredBudget exposes the same app/rule baseline and ceiling resolution
// as tracing without supplying a request-header override. It is a configured
// guest-execution budget, not an end-to-end response deadline (ADR-436).
func ConfiguredBudget(input Input, rule *api.EdgeRuleResponse) BudgetPolicyPreview {
	if !hasAppRequestBudget(input) {
		return BudgetPolicyPreview{Source: "unavailable"}
	}
	return resolveBudgetPolicy(input, rule, nil)
}
