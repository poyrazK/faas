package api

import (
	"slices"
	"testing"
)

// ADR 20261027132936 amendment 2: ai_budget_alerts is an app-scoped,
// notification-only metric with the route alert windows, gated on the AI
// gateway capability.
func TestAIBudgetAlertMetric(t *testing.T) {
	if !slices.Contains(AllowedAlertRuleMetrics, AlertMetricAIBudgetAlerts) {
		t.Fatal("ai_budget_alerts is not an allowed alert metric")
	}
	if capability, ok := AlertMetricCapability(AlertMetricAIBudgetAlerts); !ok || capability != "ai-gateway" {
		t.Fatalf("capability = %q, %v", capability, ok)
	}
	if AlertRuleActionAllowedForMetric(AlertMetricAIBudgetAlerts, "rollback") || !AlertRuleActionAllowedForMetric(AlertMetricAIBudgetAlerts, "webhook") {
		t.Fatal("ai_budget_alerts must be notification-only")
	}
	if got := AlertMetricActionRestriction(AlertMetricAIBudgetAlerts); got != "AI budget alerts support webhook action only" {
		t.Fatalf("restriction = %q", got)
	}
	for _, tc := range []struct {
		window, app string
		ok          bool
	}{
		{"15m", "app-1", true},
		{"24h", "app-1", true},
		{"15m", "", false},
		{"7d", "app-1", false},
	} {
		if prob := ValidateRouteAlert(AlertMetricAIBudgetAlerts, tc.window, tc.app); (prob == nil) != tc.ok {
			t.Errorf("window %q app %q: problem = %v, want ok=%v", tc.window, tc.app, prob, tc.ok)
		}
	}
}
