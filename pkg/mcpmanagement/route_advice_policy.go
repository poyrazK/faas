package mcpmanagement

import (
	"encoding/hex"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Preserve legacy telemetry, but never interpret missing policy evidence as
// permission to change a rule. Omit mutation recipes even from older APIs.
func prepareRouteAdvicePolicy(out *api.RouteAdviceResponse) error {
	if out.PolicyContext == nil {
		out.PolicyContext = &api.RouteAdvicePolicyContext{Status: "unavailable", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Rules: []api.RouteAdvicePolicyRule{}, Gaps: []string{"policy_context_not_provided"}}
	}
	context := out.PolicyContext
	switch context.Status {
	case "observed":
		if revision, err := hex.DecodeString(context.Revision); err != nil || len(revision) != 32 {
			return invalid("invalid_response", "policy snapshot lacks a revision")
		}
	case "unavailable", "changed":
		if context.Revision != "" || len(context.Rules) != 0 {
			return invalid("invalid_response", "unavailable policy snapshot contains rule claims")
		}
	default:
		return invalid("invalid_response", "unknown policy coverage status")
	}
	if len(context.Rules) > api.RouteAdvicePolicyRuleLimit {
		return invalid("invalid_response", "policy snapshot exceeds its rule bound")
	}
	ids := map[string]bool{}
	for _, rule := range context.Rules {
		ids[rule.ID] = true
	}
	for i := range out.Suggestions {
		suggestion := &out.Suggestions[i]
		suggestion.Rules = []api.CreateEdgeRuleRequest{}
		if suggestion.Policy == nil || context.Status != "observed" {
			suggestion.Policy = &api.RouteAdvicePolicyAssessment{Status: "unavailable", Reason: "policy_evidence_unavailable", RuleIDs: []string{}}
			if context.Status == "changed" {
				suggestion.Policy.Reason = "policy_changed_during_read"
			}
		}
		policy := suggestion.Policy
		switch policy.Status {
		case "duplicate", "conflict":
			if len(policy.RuleIDs) == 0 {
				return invalid("invalid_response", "policy comparison lacks a matching rule")
			}
		case "no_matching_rule", "review_required", "unavailable":
		default:
			return invalid("invalid_response", "unknown suggestion policy status")
		}
		if len(policy.RuleIDs) > api.RouteAdvicePolicyMatchLimit {
			return invalid("invalid_response", "policy assessment exceeds its match bound")
		}
		for _, id := range policy.RuleIDs {
			if !ids[id] {
				return invalid("invalid_response", "policy assessment names a rule outside its snapshot")
			}
		}
	}
	return nil
}
