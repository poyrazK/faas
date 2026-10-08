// adr: 570
package edgeruletrace

import "github.com/onebox-faas/faas/pkg/api"

// TotalDeadlinePolicyPreview is independent of the later execution-budget
// phase, which a cache hit, fixed response, or other gate may never reach.
// Configuration alone cannot establish deployed enforcement availability.
type TotalDeadlinePolicyPreview struct {
	RuleID            string `json:"rule_id"`
	SelectionPath     string `json:"selection_path"`
	ConfiguredMS      int64  `json:"configured_ms"`
	DeadlineMS        int64  `json:"deadline_ms"`
	PlanMaxMS         int64  `json:"plan_max_ms"`
	Status            string `json:"status"`
	EnforcementStatus string `json:"enforcement_status"`
	Scope             string `json:"scope"`
}

// Invalid total values produce owner compilation errors in the host snapshot,
// even when this request's path/method would not select that budget rule.
func unavailableTotalDeadlineRule(input Input, rules []api.EdgeRuleResponse) *api.EdgeRuleResponse {
	for i := range rules {
		rule := &rules[i]
		if !rule.Enabled || rule.Kind != "budget" || !HostMatches(rule.MatchHost, input.Host) {
			continue
		}
		action, ok := decodeAction[api.EdgeRuleBudgetAction](rule.Action, "budget")
		if ok {
			if _, valid := api.CompileBudgetActionForRuntime(*action); !valid {
				return rule
			}
		}
	}
	return nil
}

func runtimeTrafficActionAvailable(rule api.EdgeRuleResponse) bool {
	switch rule.Kind {
	case "budget":
		action, ok := decodeAction[api.EdgeRuleBudgetAction](rule.Action, "budget")
		if !ok {
			return false
		}
		_, valid := api.CompileBudgetActionForRuntime(*action)
		return valid
	case "retry":
		action, ok := decodeAction[api.EdgeRuleRetryAction](rule.Action, "retry")
		if !ok {
			return false
		}
		_, valid := api.CompileRetryActionForRuntime(*action)
		return valid
	default:
		return true
	}
}

// selectIngressBudget uses the original route and immutable ingress headers.
// Only a positive total deadline pins the later execution rule in runtime.
func selectIngressBudget(input Input, rules []api.EdgeRuleResponse) (*api.EdgeRuleResponse, *TotalDeadlinePolicyPreview, bool) {
	first, tied := firstPhaseRule(rules, "budget", input.Host, input.Path, input.Method, input.Headers)
	if first == nil {
		return nil, nil, false
	}
	if tied {
		for _, rule := range rules {
			if rule.Priority != first.Priority {
				continue
			}
			if match, _ := firstPhaseRule([]api.EdgeRuleResponse{rule}, "budget", input.Host, input.Path, input.Method, input.Headers); match != nil {
				action, _ := decodeAction[api.EdgeRuleBudgetAction](rule.Action, "budget")
				if action.TotalDeadlineMs > 0 {
					return nil, nil, true
				}
			}
		}
		return nil, nil, false
	}
	action, _ := decodeAction[api.EdgeRuleBudgetAction](first.Action, "budget")
	if action.TotalDeadlineMs <= 0 {
		return nil, nil, false
	}
	policy := &TotalDeadlinePolicyPreview{RuleID: first.ID, SelectionPath: input.Path,
		ConfiguredMS: int64(action.TotalDeadlineMs), PlanMaxMS: input.RequestBudgetMaxMS,
		Status: "needs_app_request_budget", EnforcementStatus: "unverified",
		Scope: "ordinary_public_http_and_stream_handshake"}
	if hasAppRequestBudget(input) {
		policy.DeadlineMS = min(policy.ConfiguredMS, input.RequestBudgetMaxMS)
		policy.Status = "configured_candidate"
	}
	return first, policy, false
}
