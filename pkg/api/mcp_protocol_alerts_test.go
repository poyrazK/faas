package api

import "testing"

func TestMCPProtocolAlertValidation(t *testing.T) {
	minCalls, minutes := MCPProtocolAlertDefaults(MetricMCPProtocolErrorRate, nil, nil)
	if minCalls != 20 || minutes != 10 {
		t.Fatal("wrong defaults")
	}
	for _, metric := range []string{MetricMCPProtocolErrorRate, MetricMCPProtocolP95} {
		if ValidateMCPProtocolAlert(metric, "gt", "5m", 10, minCalls, minutes) != nil {
			t.Fatal("valid alert rejected")
		}
		for _, action := range []string{"rollback", "demote", "promote"} {
			if AlertRuleActionAllowedForMetric(metric, action) {
				t.Fatal("deployment action accepted")
			}
		}
	}
	for _, tc := range []struct {
		metric, comparison, window string
		threshold                  float64
		calls, minutes             int
	}{
		{MetricMCPProtocolErrorRate, "gt", "5m", 101, 20, 10}, {MetricMCPProtocolP95, "gt", "5m", 300001, 20, 10},
		{MetricMCPProtocolP95, "lt", "5m", 10, 20, 10}, {MetricMCPProtocolP95, "gt", "7d", 10, 20, 10},
		{MetricMCPProtocolP95, "gt", "5m", 10, 19, 10}, {MetricMCPProtocolP95, "gt", "5m", 10, 20, 61},
		{"request_count", "gt", "5m", 10, 20, 10},
	} {
		if ValidateMCPProtocolAlert(tc.metric, tc.comparison, tc.window, tc.threshold, tc.calls, tc.minutes) == nil {
			t.Fatalf("invalid case accepted: %+v", tc)
		}
	}
}
