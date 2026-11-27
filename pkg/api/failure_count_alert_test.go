package api

import "testing"

// boot_failures and job_failures are notification-only: a node fault also
// fails boots and neither count is tied to one deployment, so a rollback
// could undo a healthy release.
func TestFailureCountAlertsAreWebhookOnly(t *testing.T) {
	for _, metric := range []string{AlertMetricBootFailures, AlertMetricJobFailures} {
		if !AlertRuleActionAllowedForMetric(metric, "webhook") || !AlertRuleActionAllowedForMetric(metric, "") {
			t.Errorf("%s: webhook action refused", metric)
		}
		for _, action := range []string{"rollback", "promote", "demote"} {
			if AlertRuleActionAllowedForMetric(metric, action) {
				t.Errorf("%s: %s action allowed", metric, action)
			}
		}
		if got := AlertMetricActionRestriction(metric); got == "pre-auth target metrics support webhook action only" {
			t.Errorf("%s: restriction message %q names the wrong metric", metric, got)
		}
	}
	if p := ValidateRouteAlert(AlertMetricBootFailures, "15m", ""); p == nil {
		t.Error("boot_failures without an app accepted")
	}
}
