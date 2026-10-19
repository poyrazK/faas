package issues

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func CompoundRuleHash(rule api.IssueCompoundAlertRule) string {
	rule.Enabled = true
	raw, _ := json.Marshal(rule)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// CompoundAlertDecision reports every predicate, not only the first unmet one.
// Delivery routing/subscription/capacity remain independent admission checks.
func CompoundAlertDecision(rule api.IssueCompoundAlertRule, issue api.Issue, events, customers int64, snoozed bool, last *time.Time, now time.Time) api.IssueCompoundAlertPreview {
	out := api.IssueCompoundAlertPreview{
		IssueID: issue.ID, RuleKey: rule.Key, Environment: issue.Environment,
		WindowStart: now.Add(-time.Duration(rule.WindowSeconds) * time.Second), WindowEnd: now,
		ObservedEvents: events, IdentifiedCustomers: customers, Enabled: rule.Enabled,
		EnvironmentMatches: rule.Environment == "" || rule.Environment == issue.Environment,
		EventsMatched:      events >= rule.MinimumEvents, CustomersMatched: customers >= rule.MinimumCustomers,
		Suppressed:    snoozed || issue.State == "ignored" && (issue.IgnoredUntil == nil || issue.IgnoredUntil.After(now)),
		CooldownReady: last == nil || !now.Before(last.Add(time.Duration(rule.CooldownSeconds)*time.Second)),
	}
	if !out.CooldownReady {
		next := last.Add(time.Duration(rule.CooldownSeconds) * time.Second)
		out.NextEligibleAt = &next
	}
	switch {
	case !out.Enabled:
		out.Reason = "disabled"
	case !out.EnvironmentMatches:
		out.Reason = "environment_excluded"
	case out.Suppressed:
		out.Reason = "suppressed"
	case !out.EventsMatched:
		out.Reason = "event_threshold"
	case !out.CustomersMatched:
		out.Reason = "customer_threshold"
	case !out.CooldownReady:
		out.Reason = "cooldown"
	default:
		out.Allowed, out.Reason = true, "eligible"
	}
	return out
}
