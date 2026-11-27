package agents

import (
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// validCapabilityPolicy checks a version's capability policy
// (ADR-20261027090016 amendment 1) against the plan. Fields whose rollout
// step has not shipped are refused, not ignored: a seller must never believe
// a rule is enforced that is not. A policy that allows nothing and sets no
// budget is dropped.
func validCapabilityPolicy(in *api.AgentCapabilityPolicy, plan api.Plan) (*api.AgentCapabilityPolicy, *api.Problem) {
	if in == nil {
		return nil, nil
	}
	switch {
	case len(in.Sensitive) > 0 || in.TaintMode != "":
		return nil, api.ErrValidation("capability_policy.sensitive and taint_mode are not available yet: taint ships with run approvals")
	case len(in.Web) > 0 || len(in.Data) > 0:
		return nil, api.ErrValidation("capability_policy.web and data are not available yet; allow web_fetch under tools instead")
	case len(in.Tools) > api.AgentCapabilityPolicyToolsMax:
		return nil, api.ErrValidation(fmt.Sprintf("capability_policy.tools has %d entries; at most %d are allowed",
			len(in.Tools), api.AgentCapabilityPolicyToolsMax))
	}
	out := &api.AgentCapabilityPolicy{}
	seen := map[string]bool{}
	for i, t := range in.Tools {
		switch {
		case !slices.Contains(api.AgentCapabilityTools, t.Name):
			return nil, api.ErrValidation(fmt.Sprintf("capability_policy.tools[%d].name must be one of: %s", i,
				strings.Join(api.AgentCapabilityTools, ", ")))
		case seen[t.Name]:
			return nil, api.ErrValidation(fmt.Sprintf("capability_policy.tools[%d]: %s is named twice", i, t.Name))
		case t.Mode != api.AgentCapabilityAllow && t.Mode != api.AgentCapabilityApprove && t.Mode != api.AgentCapabilityDeny:
			return nil, api.ErrValidation(fmt.Sprintf("capability_policy.tools[%d].mode must be %q, %q or %q", i,
				api.AgentCapabilityAllow, api.AgentCapabilityApprove, api.AgentCapabilityDeny))
		}
		seen[t.Name] = true
		out.Tools = append(out.Tools, api.AgentCapabilityTool{Name: t.Name, Mode: t.Mode})
	}
	if b := in.Budgets; b != nil && b.ToolCalls != 0 {
		ceiling := api.AgentToolCallsPerRunMax[plan.PlanIndex()]
		switch {
		case b.ToolCalls < 0:
			return nil, api.ErrValidation("capability_policy.budgets.tool_calls must be positive, or omitted for the plan's maximum")
		case b.ToolCalls > ceiling:
			return nil, api.ErrAgentLimit(plan, "AgentToolCallsPerRunMax", ceiling, b.ToolCalls)
		}
		out.Budgets = &api.AgentCapabilityBudgets{ToolCalls: b.ToolCalls}
	}
	if len(out.Tools) == 0 && out.Budgets == nil {
		return nil, nil
	}
	return out, nil
}
