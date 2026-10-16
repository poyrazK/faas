package operatorobs

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// AlertSuppression verifies a retained episode against fresh current rules
// before joining the exact label fingerprint to Alertmanager. Extra/external
// labels and relabeling deliberately leave the join unmatched. No HTTP read
// mutates silences, triage, collection history or notification routing.
func AlertSuppression(ctx context.Context, client *promql.Client, am *AlertmanagerClient, item api.ObsIncident, now time.Time) api.ObsAlertSuppression {
	out := api.ObsAlertSuppression{State: "unknown", Reason: "episode_unverified", CheckedAt: now.UTC()}
	if item.AlertEpisode != nil && item.AlertEpisode.ResolvedAt != nil {
		out.State, out.Reason = "not_applicable", "historical_episode"
		return out
	}
	if status := am.configuration(); status.State != "healthy" {
		out.State, out.Reason = status.State, status.Reason
		return out
	}
	if item.Type != "platform_alert" || item.Status != "firing" || item.AlertEpisode == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsNotificationBudget)
	defer cancel()
	groups, err := client.AlertRules(ctx)
	if err != nil {
		out.State, out.Reason = "unavailable", "prometheus_rules_unavailable"
		return out
	}
	snapshot, err := alertSnapshot(groups, now)
	if err != nil {
		out.State, out.Reason = "unavailable", "prometheus_rules_invalid_or_stale"
		return out
	}
	verified := false
	seriesMatches := 0
	for _, episode := range snapshot.Episodes {
		if episode.SeriesID == item.AlertEpisode.SeriesID {
			seriesMatches++
		}
		if item.ID == "platform_alert/"+episode.ID && item.AlertEpisode.SeriesID == episode.SeriesID && item.AlertEpisode.StartedAt.Equal(episode.StartedAt) {
			verified = true
		}
	}
	if seriesMatches > 1 {
		out.State, out.Reason = "unmatched", "current_identity_not_unique"
		return out
	}
	if !verified {
		out.State, out.Reason = "unmatched", "current_episode_not_found"
		return out
	}
	rows, source := am.alerts(ctx, now)
	if source.State != "healthy" {
		out.State, out.Reason = source.State, source.Reason
		return out
	}
	var matches []alertmanagerAlert
	for _, row := range rows {
		labels := make(map[string]string, len(row.Labels)+1)
		for k, v := range row.Labels {
			labels[k] = v
		}
		// These synthetic Prometheus labels must never be supplied by AM.
		if _, exists := labels["alertstate"]; exists {
			continue
		}
		if _, exists := labels["__name__"]; exists {
			continue
		}
		labels["alertstate"] = "firing"
		candidate := projectAlert(promql.VectorSample{Labels: labels, Value: 1})
		if strings.TrimPrefix(candidate.ID, "platform_alert/") == item.AlertEpisode.SeriesID {
			matches = append(matches, row)
		}
	}
	if len(matches) == 0 {
		out.State, out.Reason = "unmatched", "alertmanager_alert_not_found"
		return out
	}
	if len(matches) > 1 {
		out.State, out.Reason = "unmatched", "alertmanager_identity_not_unique"
		return out
	}
	return projectSuppression(out, matches[0], item.AlertEpisode.StartedAt, now)
}

func projectSuppression(out api.ObsAlertSuppression, row alertmanagerAlert, started, now time.Time) api.ObsAlertSuppression {
	// Alertmanager 0.27 serializes strfmt.DateTime at millisecond precision.
	// Compare the activation lower bound at that same precision, preserving
	// exact current Prometheus episode verification before this projection.
	if row.StartsAt.Before(started.Truncate(time.Millisecond)) || row.StartsAt.After(now.Add(api.ObsHealthClockSkewTolerance)) || row.UpdatedAt.Before(row.StartsAt) || row.UpdatedAt.After(now.Add(api.ObsHealthClockSkewTolerance)) || row.EndsAt.Before(row.StartsAt) {
		out.State, out.Reason = "unmatched", "episode_clock_mismatch"
		return out
	}
	updated := row.UpdatedAt.UTC()
	out.UpdatedAt = &updated
	if row.UpdatedAt.Before(now.Add(-api.ObsHealthFreshnessMaxAge)) || !row.EndsAt.After(now) {
		out.State, out.Reason = "stale", "alertmanager_observation_stale"
		return out
	}
	if row.Status.State == "unprocessed" {
		out.State, out.Reason = "unprocessed", "awaiting_alertmanager_processing"
		return out
	}
	silenced, inhibited := len(row.Status.SilencedBy) > 0, len(row.Status.InhibitedBy) > 0
	if row.Status.State == "active" && (silenced || inhibited) || row.Status.State == "suppressed" && !silenced && !inhibited {
		out.State, out.Reason = "unavailable", "inconsistent_alertmanager_status"
		return out
	}
	out.State, out.Reason = row.Status.State, ""
	out.Silenced, out.Inhibited = &silenced, &inhibited
	return out
}
