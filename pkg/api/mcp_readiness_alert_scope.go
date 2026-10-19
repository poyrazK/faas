package api

// An empty selector retains the legacy app-wide aggregation. In particular,
// default is an explicit environment, not a synonym for all environments.
func ValidateMCPReadinessAlertScope(metric, scope string) *Problem {
	if scope == "" {
		return nil
	}
	if metric != MetricMCPReadinessUnhealthy || ValidateScope(scope) != nil {
		return ErrAlertRuleInvalid("mcp_readiness_scope requires a valid environment and mcp_readiness_unhealthy metric")
	}
	return nil
}
