package operatorobs

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const AlertRecurrencePath = "/v1/admin/obs/incidents/platform_alert/recurrence"

func RecurrenceSince(raw string, now time.Time) (time.Time, error) {
	since := now.Add(-api.ObsAlertRecurrenceWindowDefault)
	if raw != "" {
		var err error
		since, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, state.ErrInvalidArgument
		}
	}
	since, now = since.UTC().Truncate(time.Microsecond), now.UTC().Truncate(time.Microsecond)
	if now.IsZero() || since.IsZero() || !since.Before(now) || since.Before(now.Add(-api.ObsAlertRecurrenceWindowMax)) {
		return time.Time{}, state.ErrInvalidArgument
	}
	return since, nil
}

func AlertRecurrence(ctx context.Context, reader state.OperatorAlertRecurrenceReader, enabled bool, since, now time.Time) (api.ObsAlertRecurrence, error) {
	out := api.ObsAlertRecurrence{AsOf: now.UTC().Truncate(time.Microsecond), Since: since.UTC().Truncate(time.Microsecond), Scope: "retained_activation_cohort", PercentileMethod: "nearest_rank_whole_seconds", Items: []api.ObsAlertRecurrenceSeries{}, Limitations: []string{"retained_episodes_only_not_lifetime_counts", "activation_cohort_excludes_older_episodes", "collection_coverage_tracks_cadence_not_continuous_rule_or_metric_availability", "resolution_is_confirmed_observation_not_exact_recovery_time", "observed_repeats_not_false_positive_or_notification_counts"}}
	if _, err := RecurrenceSince(since.Format(time.RFC3339Nano), now); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsAlertRecurrenceBudget)
	defer cancel()
	snap, err := reader.OperatorAlertRecurrence(ctx, state.OperatorAlertRecurrenceFilter{Since: out.Since, Now: out.AsOf, CollectionEnabled: enabled})
	if err != nil {
		return out, err
	}
	out.Collection, out.Retention = collectionHealth(snap.Collection, enabled, out.AsOf), retentionHealth(snap.Retention, out.AsOf)
	coverage, _, err := collectionCoverage(snap.CollectionOutcomes, out.Since, out.AsOf)
	if err != nil {
		return out, err
	}
	out.Coverage = &coverage
	if snap.Collection.LastSuccessAt.IsZero() {
		return out, nil
	}
	groups := map[recurrenceKey][]state.OperatorAlertRecurrenceEpisode{}
	for _, e := range snap.Episodes {
		key := recurrenceKey{e.RuleKey, e.SeriesID}
		groups[key] = append(groups[key], e)
	}
	total := &recurrenceAccumulator{}
	out.Totals = &api.ObsAlertRecurrenceTotals{Series: int64(len(groups))}
	for _, episodes := range groups {
		sort.Slice(episodes, func(i, j int) bool {
			if episodes[i].StartedAt.Equal(episodes[j].StartedAt) {
				return episodes[i].ID < episodes[j].ID
			}
			return episodes[i].StartedAt.Before(episodes[j].StartedAt)
		})
		if len(episodes) > 1 {
			out.Totals.RepeatedSeries++
		}
		total.add(episodes, out.AsOf)
		out.Items = append(out.Items, recurrenceSeries(episodes, out.AsOf))
	}
	out.Totals.Metrics = total.result()
	sort.Slice(out.Items, func(i, j int) bool { return recurrenceLess(out.Items[i], out.Items[j]) })
	out.Truncated = len(out.Items) > api.ObsAlertRecurrenceSeriesMax
	if out.Truncated {
		out.Items = out.Items[:api.ObsAlertRecurrenceSeriesMax]
	}
	return out, nil
}

type recurrenceKey struct{ rule, series string }

func recurrenceSeries(episodes []state.OperatorAlertRecurrenceEpisode, now time.Time) api.ObsAlertRecurrenceSeries {
	last := episodes[len(episodes)-1]
	a := &recurrenceAccumulator{}
	a.add(episodes, now)
	out := api.ObsAlertRecurrenceSeries{RuleKey: last.RuleKey, SeriesID: last.SeriesID, Name: last.Name, Severity: last.Severity, Component: last.Component, AccountID: last.AccountID, AppID: last.AppID, NodeID: last.NodeID, FirstActivationAt: episodes[0].StartedAt, LastActivationAt: last.StartedAt, Metrics: a.result(), RecentEpisodes: []api.ObsAlertRecurrenceEpisode{}, RecentEpisodesTruncated: len(episodes) > api.ObsAlertRecurrenceRecentMax}
	for i := len(episodes) - 1; i >= 0 && len(out.RecentEpisodes) < api.ObsAlertRecurrenceRecentMax; i-- {
		e := episodes[i]
		out.RecentEpisodes = append(out.RecentEpisodes, api.ObsAlertRecurrenceEpisode{EpisodeID: e.ID, Status: e.Status, StartedAt: e.StartedAt, ResolvedAt: e.ResolvedAt, EvidencePath: EvidencePath("platform_alert", e.ID)})
	}
	return out
}

func recurrenceLess(a, b api.ObsAlertRecurrenceSeries) bool {
	if a.Metrics.RepeatActivations != b.Metrics.RepeatActivations {
		return a.Metrics.RepeatActivations > b.Metrics.RepeatActivations
	}
	if !a.LastActivationAt.Equal(b.LastActivationAt) {
		return a.LastActivationAt.After(b.LastActivationAt)
	}
	if a.RuleKey != b.RuleKey {
		return a.RuleKey < b.RuleKey
	}
	return a.SeriesID < b.SeriesID
}
