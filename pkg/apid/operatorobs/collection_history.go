package operatorobs

import (
	"context"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const AlertCollectionHistoryPath = "/v1/admin/obs/collection-history"

func CollectionHistorySince(raw string, now time.Time) (time.Time, error) {
	since := now.Add(-api.ObsAlertCollectionHistoryWindowDefault)
	if raw != "" {
		var err error
		since, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, state.ErrInvalidArgument
		}
	}
	since, now = since.UTC().Truncate(time.Microsecond), now.UTC().Truncate(time.Microsecond)
	if since.IsZero() || now.IsZero() || !since.Before(now) || since.Before(now.Add(-api.ObsAlertCollectionHistoryRetention)) {
		return time.Time{}, state.ErrInvalidArgument
	}
	return since, nil
}

func CollectionHistoryQuery(raw string, forceQuery bool) (string, bool, error) {
	q, err := url.ParseQuery(raw)
	if err != nil || forceQuery {
		return "", false, state.ErrInvalidArgument
	}
	for key, values := range q {
		if len(values) != 1 || values[0] == "" || key != "since" && key != "details" {
			return "", false, state.ErrInvalidArgument
		}
	}
	if q.Has("details") && q.Get("details") != "true" && q.Get("details") != "false" {
		return "", false, state.ErrInvalidArgument
	}
	return q.Get("since"), q.Get("details") == "true", nil
}

func AlertCollectionHistory(ctx context.Context, reader state.OperatorAlertCollectionHistoryReader, enabled bool, since, now time.Time, details bool) (api.ObsAlertCollectionHistory, error) {
	out := api.ObsAlertCollectionHistory{Items: []api.ObsAlertCollectionHistoryInterval{}, Limit: api.ObsAlertCollectionHistoryIntervalsMax}
	if _, err := CollectionHistorySince(since.Format(time.RFC3339Nano), now); err != nil {
		return out, err
	}
	since, now = since.UTC().Truncate(time.Microsecond), now.UTC().Truncate(time.Microsecond)
	ctx, cancel := context.WithTimeout(ctx, api.ObsAlertCollectionHistoryBudget)
	defer cancel()
	snap, err := reader.OperatorAlertCollectionHistory(ctx, state.OperatorAlertCollectionHistoryFilter{Since: since, Now: now, IncludeDetails: details})
	if err != nil {
		return out, err
	}
	coverage, intervals, err := collectionCoverage(snap.Outcomes, since, now)
	if err != nil {
		return out, err
	}
	out.Coverage, out.Collection = coverage, collectionHealth(snap.Collection, enabled, now)
	if details {
		out.FailureDetails = &api.ObsAlertCollectionFailureDetails{Items: snap.Failures, TotalFailures: coverage.Failures, Limit: api.ObsAlertCollectionFailureDetailsMax, Truncated: coverage.Failures > int64(len(snap.Failures)), Limitations: []string{"prospective_rejected_snapshots_only", "missing_diagnostics_are_unknown", "issues_observed_not_proven_root_cause", "unreadable_sources_have_no_rule_diagnostics", "preview_bounded_counts_cover_complete_window"}}
	}
	out.TotalIntervals = len(intervals)
	out.Truncated = out.TotalIntervals > out.Limit
	for i := len(intervals) - 1; i >= 0 && len(out.Items) < out.Limit; i-- {
		out.Items = append(out.Items, intervals[i])
	}
	return out, nil
}

// Fresh outcome intervals are an explicit evidence model. Periods before
// recording, missed polls, pruning and unrecordable DB outages remain unknown.
func collectionCoverage(rows []state.OperatorAlertCollectionOutcome, since, now time.Time) (api.ObsAlertCollectionCoverage, []api.ObsAlertCollectionHistoryInterval, error) {
	out := api.ObsAlertCollectionCoverage{Since: since, AsOf: now, Scope: "recorded_collection_cadence", State: "unknown", MaxAgeSeconds: int64(api.ObsAlertCollectionOutcomeMaxAge.Seconds()), RetentionSeconds: int64(api.ObsAlertCollectionHistoryRetention.Seconds()), WindowMicroseconds: now.Sub(since).Microseconds(), Limitations: []string{"prospective_retained_outcomes_only", "fresh_outcome_intervals_are_not_continuous_availability", "expected_rule_inventory_and_metric_coverage_not_verified", "missing_outcomes_do_not_identify_the_cause"}}
	intervals := []api.ObsAlertCollectionHistoryInterval{}
	cursor := since
	for i, r := range rows {
		if r.AttemptedAt.IsZero() || r.AttemptedAt.After(now) || i > 0 && !r.AttemptedAt.After(rows[i-1].AttemptedAt) || !validCollectionOutcome(r) {
			return out, nil, state.ErrInvalidArgument
		}
		if !r.AttemptedAt.Before(since) {
			if r.Outcome == "success" {
				out.Successes++
			} else {
				out.Failures++
			}
		}
		from, until := r.AttemptedAt, r.AttemptedAt.Add(api.ObsAlertCollectionOutcomeMaxAge)
		if from.Before(since) {
			from = since
		}
		if until.After(now) {
			until = now
		}
		if i+1 < len(rows) && until.After(rows[i+1].AttemptedAt) {
			until = rows[i+1].AttemptedAt
		}
		if !until.After(from) {
			continue
		}
		if cursor.Before(from) {
			intervals = appendCollectionInterval(intervals, cursor, from, "unknown", "no_recent_outcome")
		}
		intervals = appendCollectionInterval(intervals, from, until, r.Outcome, r.Reason)
		cursor = until
	}
	if cursor.Before(now) {
		intervals = appendCollectionInterval(intervals, cursor, now, "unknown", "no_recent_outcome")
	}
	for _, interval := range intervals {
		d := interval.Until.Sub(interval.From).Microseconds()
		switch interval.State {
		case "success":
			out.SuccessfulObservationMicroseconds += d
		case "failure":
			out.FailedObservationMicroseconds += d
		default:
			out.UnknownMicroseconds += d
		}
	}
	if out.SuccessfulObservationMicroseconds+out.FailedObservationMicroseconds > 0 {
		out.State = "partial"
	}
	if out.SuccessfulObservationMicroseconds == out.WindowMicroseconds {
		out.State = "observed"
	}
	return out, intervals, nil
}

func appendCollectionInterval(items []api.ObsAlertCollectionHistoryInterval, from, until time.Time, status, reason string) []api.ObsAlertCollectionHistoryInterval {
	if len(items) > 0 {
		last := &items[len(items)-1]
		if last.Until.Equal(from) && last.State == status && last.Reason == reason {
			last.Until = until
			return items
		}
	}
	return append(items, api.ObsAlertCollectionHistoryInterval{From: from, Until: until, State: status, Reason: reason})
}

func validCollectionOutcome(r state.OperatorAlertCollectionOutcome) bool {
	if r.Outcome == "success" {
		return r.Reason == ""
	}
	if r.Outcome != "failure" {
		return false
	}
	switch r.Reason {
	case "collection_failed", "source_read_failed", "snapshot_rejected", "timeout", "persistence_failed":
		return true
	}
	return false
}
