package operatorobs

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func assessRule(group promql.AlertRuleGroup, rule promql.AlertRule, base api.ObsRuleHealthItem, now time.Time) api.ObsRuleHealthItem {
	out := base
	out.Kind, out.State = "rule", "healthy"
	out.Issues = append([]string{}, base.Issues...)
	// Matches the collector's rule key for valid definitions. Raw identity
	// material remains private; invalid definitions are never join evidence.
	out.ID = ruleHealthHash([]any{group.File, group.Name, rule.Name, rule.Query, rule.Duration, rule.Labels})
	if len(rule.Name) <= api.ObsAlertNameMaxBytes && alertNamePattern.MatchString(rule.Name) {
		out.RuleName = rule.Name
	}
	out.RunbookURL = ruleHealthRunbook(rule)
	out.HasEvaluationError = rule.LastError != ""
	switch rule.State {
	case "inactive", "pending", "firing":
		out.ReportedState = rule.State
	}
	if !rule.LastEvaluation.IsZero() {
		evaluated := rule.LastEvaluation.UTC()
		out.LastEvaluatedAt = &evaluated
		if !evaluated.After(now) {
			age := now.Unix() - evaluated.Unix()
			if now.Nanosecond() < evaluated.Nanosecond() {
				age--
			}
			out.EvaluationAgeSeconds = &age
		}
	}
	if rule.Type != "alerting" {
		out.Issues = append(out.Issues, "unexpected_rule_type")
	}
	if out.RuleName == "" || rule.Query == "" || math.IsNaN(rule.Duration) || math.IsInf(rule.Duration, 0) || rule.Duration < 0 || rule.Alerts == nil {
		out.Issues = append(out.Issues, "invalid_rule_definition")
	}
	if rule.Health == "err" || rule.LastError != "" {
		out.Issues = append(out.Issues, "evaluation_failed")
	} else if rule.Health != "ok" {
		out.Issues = append(out.Issues, "evaluation_unavailable")
	}
	switch {
	case rule.LastEvaluation.IsZero():
		out.Issues = append(out.Issues, "missing_evaluation")
	case rule.LastEvaluation.After(now.Add(api.ObsHealthClockSkewTolerance)):
		out.Issues = append(out.Issues, "evaluation_clock_in_future")
	case rule.LastEvaluation.Before(now.Add(-api.ObsHealthFreshnessMaxAge)):
		out.Issues = append(out.Issues, "evaluation_stale")
	}
	firing, pending, valid := ruleHealthAlertCounts(rule)
	if !valid {
		out.Issues = append(out.Issues, "invalid_alert_observation")
	} else {
		consistent := rule.State == "inactive" && firing+pending == 0 || rule.State == "pending" && pending > 0 && firing == 0 || rule.State == "firing" && firing > 0
		if !consistent {
			out.Issues = append(out.Issues, "inconsistent_rule_state")
		}
	}
	for _, issue := range out.Issues {
		state := "invalid"
		switch issue {
		case "evaluation_clock_in_future":
			state = "invalid_clock"
		case "evaluation_failed":
			state = "failed"
		case "evaluation_stale":
			state = "stale"
		case "missing_evaluation", "evaluation_unavailable":
			state = "unavailable"
		}
		if ruleHealthPriority(state) < ruleHealthPriority(out.State) {
			out.State = state
		}
	}
	if out.State == "healthy" {
		out.Pending, out.Firing = &pending, &firing
	}
	return out
}

func ruleHealthAlertCounts(rule promql.AlertRule) (int, int, bool) {
	firing, pending := 0, 0
	for _, alert := range rule.Alerts {
		if alert.Labels["alertname"] != rule.Name || alert.ActiveAt.IsZero() || alert.ActiveAt.After(rule.LastEvaluation) {
			return 0, 0, false
		}
		switch alert.State {
		case "firing":
			firing++
		case "pending":
			pending++
		default:
			return 0, 0, false
		}
	}
	return firing, pending, true
}
