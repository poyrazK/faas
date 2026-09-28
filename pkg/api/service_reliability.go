package api

import (
	"fmt"
	"strings"
)

// NormalizeServiceReliabilityPolicies validates a policy inventory against
// the caller's declared service bindings. A policy cannot name an undeclared
// target, and canonical lower-case names prevent duplicate aliases.
func NormalizeServiceReliabilityPolicies(raw map[string]ServiceReliabilityPolicy, bindings []AppServiceBinding) (map[string]ServiceReliabilityPolicy, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > ServiceBindingTargetsMax {
		return nil, fmt.Errorf("service_reliability exceeds %d bindings", ServiceBindingTargetsMax)
	}
	allowed := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		allowed[strings.ToLower(strings.TrimSpace(binding.Service))] = struct{}{}
	}
	out := make(map[string]ServiceReliabilityPolicy, len(raw))
	for name, policy := range raw {
		canonical := strings.ToLower(strings.TrimSpace(name))
		if _, ok := allowed[canonical]; !ok || canonical == "" {
			return nil, fmt.Errorf("service_reliability.%s must name a declared service binding", name)
		}
		if _, duplicate := out[canonical]; duplicate {
			return nil, fmt.Errorf("service_reliability.%s duplicates another binding", name)
		}
		if err := policy.Validate(); err != nil {
			return nil, fmt.Errorf("service_reliability.%s: %w", name, err)
		}
		out[canonical] = policy
	}
	return out, nil
}

func (p ServiceReliabilityPolicy) Validate() error {
	if p.TimeoutMS < 0 || p.TimeoutMS > MaxServiceReliabilityTimeoutMS {
		return fmt.Errorf("timeout_ms must be 0..%d", MaxServiceReliabilityTimeoutMS)
	}
	if p.MaxAttempts < 0 || p.MaxAttempts > EdgeRuleRetryMaxAttempts {
		return fmt.Errorf("max_attempts must be 0..%d", EdgeRuleRetryMaxAttempts)
	}
	if p.MinRemainingMS < 0 || p.MinRemainingMS > MaxEdgeRuleRetryMinRemainingMs {
		return fmt.Errorf("min_remaining_ms must be 0..%d", MaxEdgeRuleRetryMinRemainingMs)
	}
	if p.RetryBudgetPercent < 0 || p.RetryBudgetPercent > MaxEdgeRuleRetryBudgetPercent {
		return fmt.Errorf("retry_budget_percent must be 0..%d", MaxEdgeRuleRetryBudgetPercent)
	}
	if p.TimeoutMS > 0 && p.MinRemainingMS > p.TimeoutMS {
		return fmt.Errorf("min_remaining_ms cannot exceed timeout_ms")
	}
	return nil
}
