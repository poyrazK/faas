package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func validObsRuleHealth(out api.ObsRuleHealthResponse) bool {
	if out.GeneratedAt.IsZero() || out.Scope != "currently_loaded_alert_rules" || out.Items == nil || out.Limitations == nil || out.Limit != api.ObsRuleHealthItemsMax || out.MaxAgeSeconds != int(api.ObsHealthFreshnessMaxAge.Seconds()) || out.ClockSkewToleranceSeconds != int(api.ObsHealthClockSkewTolerance.Seconds()) || out.TotalItems < len(out.Items) || out.Truncated != (out.TotalItems > len(out.Items)) || len(out.Items) != min(out.TotalItems, out.Limit) {
		return false
	}
	if out.Totals == nil {
		return (out.State == "disabled" || out.State == "unavailable") && out.TotalItems == 0
	}
	t := out.Totals
	for _, n := range []int{t.Groups, t.Rules, t.Healthy, t.Failed, t.Stale, t.InvalidClock, t.Unavailable, t.Invalid, t.InvalidGroups} {
		if n < 0 {
			return false
		}
	}
	if t.Rules > api.ObsAlertSnapshotMaxRules || t.Groups > api.ObsAlertSnapshotMaxRules || t.Rules > 0 && t.Groups == 0 || t.InvalidGroups > t.Groups || t.Healthy+t.Failed+t.Stale+t.InvalidClock+t.Unavailable+t.Invalid != t.Rules || out.TotalItems < t.Rules || out.TotalItems > t.Rules+t.InvalidGroups {
		return false
	}
	switch out.State {
	case "healthy":
		if t.Rules == 0 || t.Healthy != t.Rules || t.InvalidGroups != 0 {
			return false
		}
	case "degraded":
		if t.Healthy == t.Rules && t.InvalidGroups == 0 {
			return false
		}
	case "unavailable":
		if t.Rules != 0 || t.InvalidGroups != 0 {
			return false
		}
	default:
		return false
	}
	for _, item := range out.Items {
		if !validObsRuleHealthItem(item) {
			return false
		}
	}
	return true
}

func validObsRuleHealthItem(item api.ObsRuleHealthItem) bool {
	if operatorobs.ValidateReference("platform_alert", item.ID) != nil || operatorobs.ValidateReference("platform_alert", item.GroupID) != nil || item.Issues == nil {
		return false
	}
	if item.EvaluationAgeSeconds != nil && *item.EvaluationAgeSeconds < 0 {
		return false
	}
	if item.Kind == "group" {
		return item.State == "invalid" && len(item.Issues) > 0 && item.Pending == nil && item.Firing == nil
	}
	if item.Kind != "rule" {
		return false
	}
	switch item.State {
	case "healthy":
		return len(item.Issues) == 0 && item.LastEvaluatedAt != nil && item.Pending != nil && item.Firing != nil && *item.Pending >= 0 && *item.Firing >= 0
	case "failed", "stale", "invalid_clock", "unavailable", "invalid":
		return len(item.Issues) > 0 && item.Pending == nil && item.Firing == nil
	default:
		return false
	}
}
