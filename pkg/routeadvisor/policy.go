package routeadvisor

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// PolicyRule keeps private configuration local to policy comparison.
type PolicyRule struct {
	Metadata api.RouteAdvicePolicyRule
	Action   json.RawMessage
}

// AssessPolicy uses gateway path semantics, but never invents request headers,
// identities, list contents or an order for equal-priority matches.
func AssessPolicy(s api.RouteAdviceSuggestion, rules []PolicyRule, status string) *api.RouteAdvicePolicyAssessment {
	result := &api.RouteAdvicePolicyAssessment{Status: "unavailable", Reason: "policy_read_unavailable", RuleIDs: []string{}}
	if status != "observed" {
		if status == "changed" {
			result.Reason = "policy_changed_during_read"
		}
		return result
	}
	if strings.ContainsAny(s.Route, "{}") || strings.Contains(s.Route, "__route_other__") {
		result.Status, result.Reason = "review_required", "route_template_requires_request"
		return result
	}
	var same []PolicyRule
	related := false
	for _, rule := range rules {
		match, err := api.MatchEdgeRuleKindPath(rule.Metadata.Kind, rule.Metadata.Path, s.Route)
		if err != nil {
			result.Status, result.Reason = "review_required", "invalid_policy_path"
			return result
		}
		if !match || !policyMethod(rule.Metadata.Methods, s.Method) {
			continue
		}
		if len(result.RuleIDs) < api.RouteAdvicePolicyMatchLimit {
			result.RuleIDs = append(result.RuleIDs, rule.Metadata.ID)
		} else {
			result.Truncated = true
		}
		if rule.Metadata.Kind == s.Kind {
			same = append(same, rule)
		} else {
			related = true
		}
	}
	if len(same) == 0 {
		result.Status, result.Reason = "no_matching_rule", "no_selected_app_rule_matches"
		if related {
			result.Status, result.Reason = "review_required", "overlapping_policy_requires_review"
		}
		return result
	}
	first := same[0]
	if first.Metadata.Conditional || first.Metadata.Mode != "enforce" || len(same) > 1 && same[1].Metadata.Priority == first.Metadata.Priority {
		result.Status, result.Reason = "review_required", "conditional_or_equal_priority_policy"
		return result
	}
	if len(s.Rules) == 0 || !json.Valid(first.Action) || bytes.Equal(first.Action, []byte("null")) {
		result.Status, result.Reason = "review_required", "policy_action_not_comparable"
		return result
	}
	var expected, actual any
	if json.Unmarshal(s.Rules[0].Action, &expected) != nil || json.Unmarshal(first.Action, &actual) != nil {
		result.Status, result.Reason = "review_required", "policy_action_not_comparable"
		return result
	}
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(expected)
	result.Status, result.Reason = "conflict", "matching_rule_has_different_settings"
	if bytes.Equal(a, b) {
		result.Status, result.Reason = "duplicate", "matching_rule_has_same_settings"
	}
	if related {
		result.Status, result.Reason = "review_required", "overlapping_policy_requires_review"
	}
	return result
}

func policyMethod(methods []string, method string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, candidate := range methods {
		if candidate == method {
			return true
		}
	}
	return false
}
