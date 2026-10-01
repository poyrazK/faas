// adr: 375
package api

import (
	"math"
	"net/http"
	"strconv"
	"strings"
)

// CompileBudgetActionForRuntime normalizes a stored action, without applying
// API write-time defaults. An invalid total deadline cannot be compiled.
func CompileBudgetActionForRuntime(action EdgeRuleBudgetAction) (EdgeRuleBudgetAction, bool) {
	ceiling := int(RequestBudgetMax.Milliseconds())
	if action.TotalDeadlineMs < 0 || action.TotalDeadlineMs > ceiling {
		return EdgeRuleBudgetAction{}, false
	}
	if action.BudgetMs <= 0 || action.BudgetMs > ceiling {
		action.BudgetMs = ceiling
	}
	return action, true
}

// CompileRetryActionForRuntime preserves existing stored-row semantics. In
// particular, fewer than two attempts never enables a replay, and a stored
// zero remaining-budget floor is distinct from an omitted API write field.
func CompileRetryActionForRuntime(action EdgeRuleRetryAction) (EdgeRuleRetryAction, bool) {
	if action.MaxAttempts < 2 {
		return EdgeRuleRetryAction{}, false
	}
	action.MaxAttempts = min(action.MaxAttempts, EdgeRuleRetryMaxAttempts)
	if action.MinRemainingMs < 0 || action.MinRemainingMs > MaxEdgeRuleRetryMinRemainingMs {
		action.MinRemainingMs = EdgeRuleRetryDefaultMinRemainingMs
	}
	if action.BackoffMs < 0 || action.BackoffMs > MaxEdgeRuleRetryBackoffMs {
		action.BackoffMs = 0
	}
	if action.BudgetPercent < 1 || action.BudgetPercent > MaxEdgeRuleRetryBudgetPercent {
		action.BudgetPercent = EdgeRuleRetryDefaultBudgetPercent
	}
	if action.BudgetMinRetries < 0 || action.BudgetMinRetries > MaxEdgeRuleRetryBudgetMin {
		action.BudgetMinRetries = EdgeRuleRetryDefaultBudgetMin
	}
	return action, true
}

// RequestRetryMethodEligibility is the deterministic method guard only. A key
// permits an opted-in POST/PATCH to be considered; it does not deduplicate work.
func RequestRetryMethodEligibility(method string, allowNonIdempotent bool, key string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodPut, http.MethodDelete:
		return "idempotent_method"
	case http.MethodPost, http.MethodPatch:
		if !allowNonIdempotent {
			return "non_idempotent_disabled"
		}
		if strings.TrimSpace(key) == "" {
			return "idempotency_key_required"
		}
		return "non_idempotent_allowed_with_key"
	default:
		return "unsupported_method"
	}
}

// RequestExecutionBudget describes configuration, not elapsed time or a live
// admission verdict. The separate total deadline may tighten this budget.
type RequestExecutionBudget struct {
	ConfiguredMS   int64
	BudgetMS       int64
	PlanMaxMS      int64
	Source         string
	OverrideHeader string
	OverrideStatus string
}

// ResolveRequestExecutionBudget is shared by forwarding and read-only preview.
// Clamp integers before converting to time.Duration: multiplication of an
// untrusted positive override can otherwise wrap into a valid short timeout.
func ResolveRequestExecutionBudget(defaultMS, ceilingMS int64, timeoutS int, rule *EdgeRuleBudgetAction, headers http.Header) RequestExecutionBudget {
	policy := RequestExecutionBudget{ConfiguredMS: defaultMS, PlanMaxMS: ceilingMS,
		Source: "plan_default", OverrideStatus: "not_applicable"}
	if timeoutS > 0 {
		policy.Source = "app"
		if int64(timeoutS) > math.MaxInt64/1000 {
			policy.ConfiguredMS = math.MaxInt64
		} else {
			policy.ConfiguredMS = int64(timeoutS) * 1000
		}
	}
	candidate := policy.ConfiguredMS
	if rule != nil {
		policy.ConfiguredMS, policy.Source = int64(rule.BudgetMs), "rule"
		candidate = policy.ConfiguredMS
		if candidate <= 0 || candidate > RequestBudgetMax.Milliseconds() {
			candidate = RequestBudgetMax.Milliseconds()
		}
		policy.OverrideHeader = rule.AllowOverrideHeader
		if policy.OverrideHeader == "" {
			policy.OverrideHeader = RequestBudgetDefaultOverrideHeader
		}
		policy.OverrideStatus = "not_present"
		if value := headers.Get(policy.OverrideHeader); value != "" {
			policy.OverrideStatus = "ignored_invalid"
			if parsed, ok := ParseRequestBudgetOverrideMS(value); ok {
				candidate, policy.Source, policy.OverrideStatus = int64(parsed), "header_override", "applied"
			}
		}
	}
	if candidate <= 0 || candidate > ceilingMS {
		candidate = ceilingMS
		if rule != nil {
			policy.Source = "ceiling_clamp"
		}
		if policy.OverrideStatus == "applied" {
			policy.OverrideStatus = "applied_clamped"
		}
	}
	policy.BudgetMS = candidate
	return policy
}

// ParseRequestBudgetOverrideMS accepts positive decimal integers only. The
// caller applies the plan ceiling before any duration multiplication.
func ParseRequestBudgetOverrideMS(value string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
