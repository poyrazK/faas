package api

import "testing"

// job_failures is notification-only: it counts the whole account's jobs, so
// no app action fits it. boot_failures may roll back a completed release
// (ADR-605 amendment 2) but never acts on a canary.
func TestFailureCountAlertActions(t *testing.T) {
	for _, test := range []struct {
		metric, action string
		want           bool
	}{
		{AlertMetricJobFailures, "", true},
		{AlertMetricJobFailures, "webhook", true},
		{AlertMetricJobFailures, "rollback", false},
		{AlertMetricJobFailures, "demote", false},
		{AlertMetricJobFailures, "promote", false},
		{AlertMetricBootFailures, "", true},
		{AlertMetricBootFailures, "webhook", true},
		{AlertMetricBootFailures, "rollback", true},
		{AlertMetricBootFailures, "demote", false},
		{AlertMetricBootFailures, "promote", false},
	} {
		if got := AlertRuleActionAllowedForMetric(test.metric, test.action); got != test.want {
			t.Errorf("%s %q allowed = %t, want %t", test.metric, test.action, got, test.want)
		}
	}
	for _, metric := range []string{AlertMetricBootFailures, AlertMetricJobFailures} {
		if got := AlertMetricActionRestriction(metric); got == "pre-auth target metrics support webhook action only" {
			t.Errorf("%s: restriction message %q names the wrong metric", metric, got)
		}
	}
	if p := ValidateRouteAlert(AlertMetricBootFailures, "15m", ""); p == nil {
		t.Error("boot_failures without an app accepted")
	}
}
