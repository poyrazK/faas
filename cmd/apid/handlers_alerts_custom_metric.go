package main

import (
	"net/http"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// validateCustomMetricAlert enforces the ADR-745 custom_metric rule shape at
// the API boundary, mirroring alert_rules_custom_metric_chk so a bad body is
// a clean 400 rather than a constraint violation: the metric needs a valid
// custom_metric_name, every other metric needs it empty, and a custom_metric
// rule is webhook-only over a window of at most 24h. While custom metric
// history is disabled there is no exported series to evaluate, so creating
// such a rule is refused rather than left permanently unknown.
func validateCustomMetricAlert(metric, name, window string, action *string, historyEnabled bool) *api.Problem {
	if metric != api.AlertRuleMetricCustomMetric {
		if name != "" {
			return api.ErrAlertRuleInvalid("custom_metric_name must be empty when metric is not custom_metric")
		}
		return nil
	}
	if !historyEnabled {
		return api.NewProblem(http.StatusServiceUnavailable, "custom_metric_history_unavailable",
			"Custom metric history unavailable", "custom metric alerts need custom metric history, which is not enabled for this deployment")
	}
	if name == "" {
		return api.ErrAlertRuleInvalid("custom_metric_name must be set when metric is custom_metric")
	}
	if api.ValidateCustomMetricName(name) != nil {
		return api.ErrAlertRuleInvalid("custom_metric_name must match [a-z][a-z0-9_]{0,62}")
	}
	if action != nil && *action != "webhook" {
		return api.ErrAlertRuleInvalid("custom_metric alerts support webhook action only")
	}
	return validateCustomMetricAlertWindow(window)
}

func validateCustomMetricAlertWindow(window string) *api.Problem {
	if !slices.Contains(api.CustomMetricAlertWindowSpecs, window) {
		return api.ErrAlertRuleInvalid("custom_metric window_spec must be one of " + strings.Join(api.CustomMetricAlertWindowSpecs, ", "))
	}
	return nil
}
