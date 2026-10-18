package state

import "github.com/onebox-faas/faas/pkg/api"

func defaultMCPProtocolAlert(r AlertRule) AlertRule {
	if api.IsMCPProtocolMetric(string(r.Metric)) {
		if r.MCPMinCalls == 0 {
			r.MCPMinCalls = api.MCPProtocolDefaultMinCalls
		}
		if r.MCPForMinutes == 0 {
			r.MCPForMinutes = api.MCPProtocolDefaultForMinutes
		}
	}
	return r
}
func validMCPProtocolAlert(r AlertRule) bool {
	if api.ValidateMCPReadinessAlertScope(string(r.Metric), r.MCPReadinessScope) != nil {
		return false
	}
	if string(r.Metric) == api.MetricMCPReadinessUnhealthy {
		return r.AppID != "" && (r.Action == "" || r.Action == AlertActionWebhook)
	}
	if api.ValidateMCPProtocolAlert(string(r.Metric), string(r.Comparison), string(r.WindowSpec), r.Threshold, r.MCPMinCalls, r.MCPForMinutes) != nil {
		return false
	}
	return !api.IsMCPProtocolMetric(string(r.Metric)) || r.AppID != "" && (r.Action == "" || r.Action == AlertActionWebhook)
}
