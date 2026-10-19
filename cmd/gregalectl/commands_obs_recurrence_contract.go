package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func validObsRecurrence(out api.ObsAlertRecurrence) bool {
	if out.AsOf.IsZero() || out.Since.IsZero() || !out.Since.Before(out.AsOf) || out.AsOf.Sub(out.Since) > api.ObsAlertRecurrenceWindowMax || out.Scope != "retained_activation_cohort" || out.PercentileMethod != "nearest_rank_whole_seconds" || out.Items == nil || out.Limitations == nil || out.Collection.State == "" || out.Collection.CollectionState == "" || out.Retention.State == "" {
		return false
	}
	if out.Coverage != nil && (!validCollectionCoverage(*out.Coverage) || !out.Coverage.Since.Equal(out.Since) || !out.Coverage.AsOf.Equal(out.AsOf)) {
		return false
	}
	if out.Totals == nil {
		return out.Collection.LastSuccessAt == nil && len(out.Items) == 0 && !out.Truncated
	}
	t := out.Totals
	if out.Collection.LastSuccessAt == nil || !validRecurrenceMetrics(t.Metrics) || t.Series < 0 || t.Series > t.Metrics.Episodes || t.RepeatedSeries < 0 || t.RepeatedSeries > t.Series || t.RepeatedSeries > t.Metrics.RepeatActivations || t.Metrics.RepeatActivations != t.Metrics.Episodes-t.Series || len(out.Items) != min(int(t.Series), api.ObsAlertRecurrenceSeriesMax) || out.Truncated != (t.Series > int64(len(out.Items))) {
		return false
	}
	seen := map[string]bool{}
	var episodes, repeats, repeated int64
	for _, item := range out.Items {
		key := item.RuleKey + item.SeriesID
		if seen[key] || operatorobs.ValidateReference("platform_alert", item.RuleKey) != nil || operatorobs.ValidateReference("platform_alert", item.SeriesID) != nil || !validRecurrenceMetrics(item.Metrics) || item.Metrics.Episodes < 1 || item.Metrics.RepeatActivations != item.Metrics.Episodes-1 || item.FirstActivationAt.Before(out.Since) || item.LastActivationAt.After(out.AsOf) || item.LastActivationAt.Before(item.FirstActivationAt) || item.RecentEpisodes == nil || len(item.RecentEpisodes) != min(int(item.Metrics.Episodes), api.ObsAlertRecurrenceRecentMax) || item.RecentEpisodesTruncated != (item.Metrics.Episodes > int64(len(item.RecentEpisodes))) {
			return false
		}
		seen[key] = true
		episodes += item.Metrics.Episodes
		repeats += item.Metrics.RepeatActivations
		if item.Metrics.RepeatActivations > 0 {
			repeated++
		}
		for i, e := range item.RecentEpisodes {
			if operatorobs.ValidateReference("platform_alert", e.EpisodeID) != nil || e.EvidencePath != operatorobs.EvidencePath("platform_alert", e.EpisodeID) || e.StartedAt.Before(item.FirstActivationAt) || e.StartedAt.After(item.LastActivationAt) || i == 0 && !e.StartedAt.Equal(item.LastActivationAt) || i > 0 && !e.StartedAt.Before(item.RecentEpisodes[i-1].StartedAt) {
				return false
			}
			if e.Status != "resolved" && e.Status != "firing" && e.Status != "unknown" || (e.Status == "resolved") != (e.ResolvedAt != nil) {
				return false
			}
		}
	}
	return episodes <= t.Metrics.Episodes && repeats <= t.Metrics.RepeatActivations && repeated <= t.RepeatedSeries && (out.Truncated || episodes == t.Metrics.Episodes && repeats == t.Metrics.RepeatActivations && repeated == t.RepeatedSeries)
}

func validRecurrenceMetrics(m api.ObsAlertRecurrenceMetrics) bool {
	d, g := m.Duration, m.RecoveryGap
	for _, n := range []int64{m.Episodes, m.RepeatActivations, m.Unresolved, m.UnknownObservation, d.Samples, d.NotObserved, d.InvalidClock, g.Samples, g.UnconfirmedRecovery, g.ResolutionOverlap, g.InvalidClock} {
		if n < 0 || n > api.ObsAlertRecurrenceEpisodesMax {
			return false
		}
	}
	return m.UnknownObservation <= m.Unresolved && m.Unresolved <= m.Episodes && m.RepeatActivations <= m.Episodes && d.NotObserved == m.Unresolved && d.Samples+d.NotObserved+d.InvalidClock == m.Episodes && g.Samples+g.UnconfirmedRecovery+g.ResolutionOverlap+g.InvalidClock == m.RepeatActivations && validRecurrenceStatistics(d.Samples, d.MinSeconds, d.P50Seconds) && validRecurrenceStatistics(g.Samples, g.MinSeconds, g.P50Seconds)
}

func validRecurrenceStatistics(samples int64, minimum, median *int64) bool {
	if samples == 0 {
		return minimum == nil && median == nil
	}
	return minimum != nil && median != nil && *minimum >= 0 && *median >= *minimum && *median <= int64(api.ObsAlertRecurrenceWindowMax.Seconds())
}
