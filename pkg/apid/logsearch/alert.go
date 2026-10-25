package logsearch

import (
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ValidateAlertQuery checks a log_search_matches rule (ADR-20261018083536)
// and returns its log_query in canonical form. Every other metric must leave
// log_query empty. The rule is webhook-only with windows up to 24 hours,
// mirroring alert_rules_log_search_chk; enabled is the log search gate.
func ValidateAlertQuery(metric, query, window string, action *string, enabled bool) (string, *api.Problem) {
	if metric != api.AlertRuleMetricLogSearchMatches {
		if query != "" {
			return "", api.ErrAlertRuleInvalid("log_query must be empty when metric is not log_search_matches")
		}
		return "", nil
	}
	if !enabled {
		return "", api.ErrLogSearchUnavailable()
	}
	if strings.TrimSpace(query) == "" {
		return "", api.ErrAlertRuleInvalid("log_query must be set when metric is log_search_matches")
	}
	canonical, _, err := state.CanonicalLogQuery(query)
	if err != nil {
		return "", api.ErrAlertRuleInvalid("log_query: " + err.Error())
	}
	if action != nil && *action != "" && *action != "webhook" {
		return "", api.ErrAlertRuleInvalid(api.AlertMetricActionRestriction(metric))
	}
	if prob := ValidateAlertWindow(window); prob != nil {
		return "", prob
	}
	return canonical, nil
}

// ValidateAlertWindow is the window bound a log_search_matches rule keeps on
// update too.
func ValidateAlertWindow(window string) *api.Problem {
	if !slices.Contains(api.LogSearchAlertWindowSpecs, window) {
		return api.ErrAlertRuleInvalid("log_search_matches window_spec must be one of " + strings.Join(api.LogSearchAlertWindowSpecs, ", "))
	}
	return nil
}
