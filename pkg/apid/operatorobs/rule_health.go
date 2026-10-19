package operatorobs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

const RuleHealthPath = "/v1/admin/obs/rule-health"

var ruleHealthIdentityPattern = regexp.MustCompile(`^[a-zA-Z0-9_.:-]+$`)
var ruleHealthRunbookPattern = regexp.MustCompile(`^[a-zA-Z0-9:/_.%#~-]+$`)

// RuleHealth reads the same bounded alert-rule envelope used by the collector.
// It assesses all rules before limiting the metadata preview, never writes
// history, and never changes the collector's recovery acceptance policy.
func RuleHealth(ctx context.Context, client *promql.Client, now time.Time) api.ObsRuleHealthResponse {
	out := api.ObsRuleHealthResponse{GeneratedAt: now.UTC(), Scope: "currently_loaded_alert_rules", State: "disabled", Reason: "not_configured",
		MaxAgeSeconds: int(api.ObsHealthFreshnessMaxAge.Seconds()), ClockSkewToleranceSeconds: int(api.ObsHealthClockSkewTolerance.Seconds()),
		Items: []api.ObsRuleHealthItem{}, Limit: api.ObsRuleHealthItemsMax,
		Limitations: []string{"currently_loaded_rules_only_not_expected_inventory", "current_read_not_historical_collection_failure_cause", "evaluation_health_not_signal_or_notification_coverage", "preview_bounded_totals_cover_complete_source", "counts_unknown_for_unhealthy_rules", "raw_queries_errors_labels_and_absolute_paths_excluded"}}
	if client == nil || client.BaseURL() == "" {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsRuleHealthBudget)
	defer cancel()
	groups, err := client.AlertRules(ctx)
	if err != nil {
		out.State, out.Reason = "unavailable", "source_unavailable"
		return out
	}
	assessed := assessRuleHealth(groups, out, now)
	if ctx.Err() != nil {
		out.State, out.Reason = "unavailable", "deadline_exceeded"
		return out
	}
	return assessed
}

func assessRuleHealth(groups []promql.AlertRuleGroup, out api.ObsRuleHealthResponse, now time.Time) api.ObsRuleHealthResponse {
	out.Totals = &api.ObsRuleHealthTotals{Groups: len(groups)}
	groupCounts := map[string]int{}
	for _, group := range groups {
		groupCounts[ruleHealthHash([]string{group.File, group.Name})]++
	}
	for _, group := range groups {
		base := ruleHealthGroup(group)
		if group.File == "" || group.Name == "" {
			base.Issues = append(base.Issues, "invalid_group_identity")
		}
		if groupCounts[base.GroupID] > 1 {
			base.Issues = append(base.Issues, "duplicate_group_identity")
		}
		if len(base.Issues) > 0 {
			out.Totals.InvalidGroups++
			if len(group.Rules) == 0 {
				base.Kind, base.ID, base.State = "group", base.GroupID, "invalid"
				out.Items = append(out.Items, base)
			}
		}
		for _, rule := range group.Rules {
			out.Items = append(out.Items, assessRule(group, rule, base, now))
		}
	}
	// Ambiguous rule identities cannot establish a healthy snapshot, even if
	// each duplicate row is individually well-formed.
	ids := map[string]int{}
	for _, item := range out.Items {
		if item.Kind == "rule" {
			ids[item.ID]++
		}
	}
	for i := range out.Items {
		item := &out.Items[i]
		if item.Kind != "rule" {
			continue
		}
		if ids[item.ID] > 1 {
			item.Issues = append(item.Issues, "duplicate_rule_identity")
			item.State = "invalid"
			item.Pending, item.Firing = nil, nil
		}
		countRuleHealth(out.Totals, item.State)
	}
	out.State, out.Reason = "healthy", ""
	if out.Totals.InvalidGroups > 0 || out.Totals.Healthy != out.Totals.Rules {
		out.State, out.Reason = "degraded", "rule_diagnostics_present"
	}
	if out.Totals.Rules == 0 && out.Totals.InvalidGroups == 0 {
		out.State, out.Reason = "unavailable", "no_alert_rules_observed"
	}
	sort.SliceStable(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if ruleHealthPriority(a.State) != ruleHealthPriority(b.State) {
			return ruleHealthPriority(a.State) < ruleHealthPriority(b.State)
		}
		if a.GroupID != b.GroupID {
			return a.GroupID < b.GroupID
		}
		return a.ID < b.ID
	})
	out.TotalItems = len(out.Items)
	out.Truncated = len(out.Items) > out.Limit
	if out.Truncated {
		out.Items = out.Items[:out.Limit]
	}
	return out
}

func ruleHealthGroup(group promql.AlertRuleGroup) api.ObsRuleHealthItem {
	fileName := ""
	if group.File != "" {
		fileName = ruleHealthSafeIdentity(path.Base(group.File))
	}
	return api.ObsRuleHealthItem{GroupID: ruleHealthHash([]string{group.File, group.Name}), GroupName: ruleHealthSafeIdentity(group.Name),
		FileName: fileName, ReportedState: "unknown", Issues: []string{}}
}

func ruleHealthSafeIdentity(value string) string {
	if len(value) > api.ObsRuleHealthIdentityMaxBytes || !ruleHealthIdentityPattern.MatchString(value) {
		return ""
	}
	return value
}

func ruleHealthHash(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		encoded = []byte("invalid_rule_identity")
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func ruleHealthRunbook(rule promql.AlertRule) string {
	book := safeRunbookURL(rule.Annotations["runbook_url"])
	if len(book) > api.ObsRuleHealthRunbookMaxBytes || !ruleHealthRunbookPattern.MatchString(book) {
		return ""
	}
	return book
}

func ruleHealthPriority(state string) int {
	switch state {
	case "invalid":
		return 0
	case "invalid_clock":
		return 1
	case "failed":
		return 2
	case "stale":
		return 3
	case "unavailable":
		return 4
	default:
		return 5
	}
}

func countRuleHealth(totals *api.ObsRuleHealthTotals, state string) {
	totals.Rules++
	switch state {
	case "healthy":
		totals.Healthy++
	case "failed":
		totals.Failed++
	case "stale":
		totals.Stale++
	case "invalid_clock":
		totals.InvalidClock++
	case "unavailable":
		totals.Unavailable++
	default:
		totals.Invalid++
	}
}
