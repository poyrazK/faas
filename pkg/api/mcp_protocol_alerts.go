package api

const (
	MetricMCPProtocolErrorRate   = "mcp_protocol_error_rate_pct"
	MetricMCPProtocolP95         = "mcp_protocol_execution_p95_ms"
	MCPProtocolDefaultMinCalls   = 20
	MCPProtocolDefaultForMinutes = 10
)

func IsMCPProtocolMetric(metric string) bool {
	return metric == MetricMCPProtocolErrorRate || metric == MetricMCPProtocolP95
}
func MCPProtocolAlertDefaults(metric string, minCalls, forMinutes *int) (int, int) {
	minValue, forValue := 0, 0
	if IsMCPProtocolMetric(metric) {
		minValue = MCPProtocolDefaultMinCalls
		forValue = MCPProtocolDefaultForMinutes
	}
	if minCalls != nil {
		minValue = *minCalls
	}
	if forMinutes != nil {
		forValue = *forMinutes
	}
	return minValue, forValue
}
func ValidateMCPProtocolAlert(metric, comparison, window string, threshold float64, minCalls, forMinutes int) *Problem {
	if !IsMCPProtocolMetric(metric) {
		if minCalls != 0 || forMinutes != 0 {
			return ErrAlertRuleInvalid("MCP alert settings require an MCP protocol metric")
		}
		return nil
	}
	if minCalls < 20 || minCalls > 1000000 || forMinutes < 1 || forMinutes > 60 {
		return ErrAlertRuleInvalid("MCP minimum calls must be 20..1000000 and sustained duration must be 1..60 minutes")
	}
	if comparison != "gt" && comparison != "gte" {
		return ErrAlertRuleInvalid("MCP performance alerts require gt or gte")
	}
	if window != "5m" && window != "15m" && window != "1h" {
		return ErrAlertRuleInvalid("MCP performance windows must be 5m, 15m, or 1h")
	}
	if !IsFiniteFloat(threshold) || threshold < 0 || metric == MetricMCPProtocolErrorRate && threshold > 100 || metric == MetricMCPProtocolP95 && threshold > 300000 {
		return ErrAlertRuleInvalid("MCP threshold must be 0..100 percent or 0..300000 milliseconds")
	}
	return nil
}
