package routerequirements

import (
	"bytes"
	"encoding/json"
	"math"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

func proposedAction(checks Checks, kind string, input edgeruletrace.Input, selected *api.EdgeRuleResponse, options PlanOptions, limits api.Limits) (json.RawMessage, string) {
	if kind == "throttle" {
		return proposedThrottle(*checks.Throttle, selected, options, limits)
	}
	return proposedBudget(*checks.Budget, input, selected)
}

func proposedThrottle(want ThrottleRequirement, selected *api.EdgeRuleResponse, options PlanOptions, limits api.Limits) (json.RawMessage, string) {
	var action api.EdgeRuleThrottleAction
	if selected != nil {
		var envelope struct {
			Kind     string                     `json:"kind,omitempty"`
			Throttle api.EdgeRuleThrottleAction `json:"throttle"`
		}
		if !strictPlanAction(selected.Action, &envelope) || (envelope.Kind != "" && envelope.Kind != "throttle") {
			return nil, "unrecognized_action_fields"
		}
		action = envelope.Throttle
		if action.JWTClaimName != "" {
			return nil, "custom_throttle_claim_needs_review"
		}
	} else {
		if want.MaxRPS == nil {
			return nil, "throttle_rate_required"
		}
		if options.ThrottleBurst <= 0 {
			return nil, "throttle_burst_required"
		}
		action.RequestsPerSecond, action.Burst = *want.MaxRPS, options.ThrottleBurst
	}
	if want.MaxRPS != nil {
		if *want.MaxRPS < 1 {
			return nil, "rate_below_gateway_minimum"
		}
		action.RequestsPerSecond = math.Min(action.RequestsPerSecond, *want.MaxRPS)
	}
	action.KeyBy = want.KeyBy
	if want.KeyBy == api.ThrottleKeyByNone {
		action.MaxKeysPerRule, action.MissingKeyPolicy = 0, ""
	} else if want.MissingKeyPolicy != "" {
		action.MissingKeyPolicy = want.MissingKeyPolicy
	}
	// Validate against the account's actual named plan, not platform maxima.
	if action.Validate(api.ThrottleValidationContext{PlanMaxRPS: float64(limits.RateLimitRPS),
		PlanMaxBurst: limits.RateLimitBurst, PlanMaxKeysPerRule: limits.ThrottleMaxKeysPerRule}) != nil {
		return nil, "throttle_plan_limits"
	}
	body, _ := json.Marshal(map[string]any{"throttle": action})
	return body, ""
}

func proposedBudget(want BudgetRequirement, input edgeruletrace.Input, selected *api.EdgeRuleResponse) (json.RawMessage, string) {
	var action api.EdgeRuleBudgetAction
	if selected != nil {
		var envelope struct {
			Kind   string                   `json:"kind,omitempty"`
			Budget api.EdgeRuleBudgetAction `json:"budget"`
		}
		if !strictPlanAction(selected.Action, &envelope) || (envelope.Kind != "" && envelope.Kind != "budget") {
			return nil, "unrecognized_action_fields"
		}
		action = envelope.Budget
		if action.AllowOverrideHeader != "" {
			return nil, "custom_budget_override_needs_review"
		}
	} else {
		policy := edgeruletrace.ConfiguredBudget(input, nil)
		if policy.Source == "unavailable" {
			return nil, "budget_limits_unavailable"
		}
		// Make the existing baseline explicit without increasing it.
		action.BudgetMs = int(policy.BudgetMS)
	}
	if want.MaxMS != nil && int64(action.BudgetMs) > *want.MaxMS {
		action.BudgetMs = int(*want.MaxMS)
	}
	if action.Validate() != nil {
		return nil, "invalid_proposed_budget"
	}
	body, _ := json.Marshal(map[string]any{"budget": action})
	return body, ""
}

// Do not silently erase new action fields or copy arbitrary configuration into
// a shareable patch. Current numeric/closed-vocabulary fields are allowlisted.
func strictPlanAction(body []byte, into any) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	return decoder.Decode(into) == nil
}

func planBlockerReason(code string) string {
	switch code {
	case "overlapping_requirements_conflict", "overlapping_proposals_conflict":
		return "Overlapping requirements or generated selectors need incompatible policy changes."
	case "public_exception_would_change":
		return "The proposed selector would change the selected policy of an explicit public exception."
	case "impact_unavailable":
		return "The planner cannot prove the selected policy before and after for every intersecting captured family."
	case "plan_limits_unavailable":
		return "The account's current named plan could not be established for entitlement and quota checks."
	case "throttle_rate_required":
		return "No selected throttle supplies a rate and the requirements do not specify max_rps."
	case "throttle_burst_required":
		return "A new throttle needs an explicit burst; the requirements do not choose one."
	case "rate_below_gateway_minimum":
		return "The gateway floors throttle rates to 1 RPS, above the requested maximum."
	case "throttle_plan_limits":
		return "The proposed throttle exceeds a bundled limit or requires a key dimension unavailable on the account's named plan."
	case "priority_space_exhausted":
		return "A broader matching rule already has priority 0, leaving no lower numeric priority for an exact override."
	case "invalid_rule_priority":
		return "The selected rule's priority is outside the existing API range."
	case "app_rule_quota_reached", "throttle_rule_quota_reached":
		return "Creating an exact override would exceed a bundled app or throttle rule quota."
	case "custom_budget_override_needs_review":
		return "The selected budget uses a custom override header; its request-header behavior needs a separate review."
	case "custom_throttle_claim_needs_review":
		return "The selected throttle uses a custom JWT claim; changing that identity source needs a separate review."
	case "unrecognized_action_fields":
		return "The selected action contains fields the planner cannot preserve or safely export."
	case "budget_limits_unavailable":
		return "The configured budget baseline or ceiling is unavailable."
	default:
		return "The proposed configuration could not establish the requirement without an unresolved change."
	}
}

func planBlockerAction(code string) string {
	switch code {
	case "throttle_rate_required":
		return "Choose throttle.max_rps in the requirements and rerun the planner."
	case "throttle_burst_required":
		return "Choose a positive --throttle-burst for new throttles and rerun the planner."
	case "rate_below_gateway_minimum":
		return "Review a maximum of at least 1 RPS or enforce a lower rate in application code."
	case "priority_space_exhausted":
		return "Review the broader rule's priority and affected routes before moving it; the planner leaves it unchanged."
	case "app_rule_quota_reached", "throttle_rule_quota_reached":
		return "Review unused rules or plan capacity before creating another rule."
	case "plan_limits_unavailable", "throttle_plan_limits":
		return "Inspect the current account plan and its limits before changing policy."
	default:
		return "Inspect the selected policy with edge-rules trace and review the remaining setting manually."
	}
}
