package api

import "testing"

func TestMCPReadinessAlertScopeValidation(t *testing.T) {
	for _, tc := range []struct {
		metric, scope string
		valid         bool
	}{
		{MetricMCPReadinessUnhealthy, "", true}, {MetricMCPReadinessUnhealthy, "default", true}, {MetricMCPReadinessUnhealthy, "staging", true},
		{MetricMCPReadinessUnhealthy, "__all__", false}, {MetricMCPReadinessUnhealthy, "prod-", false}, {MetricMCPReadinessUnhealthy, "Stage", false},
		{MetricMCPProtocolErrorRate, "staging", false}, {"error_rate_pct", "staging", false}, {"error_rate_pct", "", true},
	} {
		if got := ValidateMCPReadinessAlertScope(tc.metric, tc.scope) == nil; got != tc.valid {
			t.Fatalf("%s/%s valid=%v", tc.metric, tc.scope, got)
		}
	}
	response := AlertRuleResponseFromRow(AlertRuleRow{MCPReadinessScope: "staging"})
	if response.MCPReadinessScope != "staging" {
		t.Fatal("scope lost at API boundary")
	}
}
