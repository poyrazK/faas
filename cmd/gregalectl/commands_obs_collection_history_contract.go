package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func validCollectionCoverage(c api.ObsAlertCollectionCoverage) bool {
	if c.Since.IsZero() || c.AsOf.IsZero() || !c.Since.Before(c.AsOf) || c.AsOf.Sub(c.Since) > api.ObsAlertCollectionHistoryRetention || c.Scope != "recorded_collection_cadence" || c.MaxAgeSeconds != int64(api.ObsAlertCollectionOutcomeMaxAge.Seconds()) || c.RetentionSeconds != int64(api.ObsAlertCollectionHistoryRetention.Seconds()) || c.Limitations == nil || c.WindowMicroseconds != c.AsOf.Sub(c.Since).Microseconds() {
		return false
	}
	for _, n := range []int64{c.SuccessfulObservationMicroseconds, c.FailedObservationMicroseconds, c.UnknownMicroseconds} {
		if n < 0 || n > c.WindowMicroseconds {
			return false
		}
	}
	if c.SuccessfulObservationMicroseconds+c.FailedObservationMicroseconds+c.UnknownMicroseconds != c.WindowMicroseconds || c.Successes < 0 || c.Failures < 0 || c.Successes > api.ObsAlertCollectionHistoryOutcomesMax || c.Failures > api.ObsAlertCollectionHistoryOutcomesMax || c.Successes+c.Failures > api.ObsAlertCollectionHistoryOutcomesMax {
		return false
	}
	expected := "partial"
	if c.UnknownMicroseconds == c.WindowMicroseconds {
		expected = "unknown"
	}
	if c.SuccessfulObservationMicroseconds == c.WindowMicroseconds {
		expected = "observed"
	}
	return c.State == expected
}

func validObsCollectionHistory(out api.ObsAlertCollectionHistory) bool {
	c := out.Coverage
	if !validCollectionCoverage(c) || out.Collection.State == "" || out.Collection.CollectionState == "" || out.Items == nil || out.Limit != api.ObsAlertCollectionHistoryIntervalsMax || out.TotalIntervals < 1 || out.TotalIntervals > api.ObsAlertCollectionHistoryOutcomesMax*2+1 || len(out.Items) != min(out.TotalIntervals, out.Limit) || out.Truncated != (out.TotalIntervals > len(out.Items)) {
		return false
	}
	if out.FailureDetails != nil && !validCollectionFailureDetails(*out.FailureDetails, c) {
		return false
	}
	var success, failure, unknown int64
	for i, item := range out.Items {
		if !item.From.Before(item.Until) || item.From.Before(c.Since) || item.Until.After(c.AsOf) || i == 0 && !item.Until.Equal(c.AsOf) || i > 0 && !item.Until.Equal(out.Items[i-1].From) {
			return false
		}
		d := item.Until.Sub(item.From).Microseconds()
		switch item.State {
		case "success":
			if item.Reason != "" {
				return false
			}
			success += d
		case "unknown":
			if item.Reason != "no_recent_outcome" {
				return false
			}
			unknown += d
		case "failure":
			switch item.Reason {
			case "collection_failed", "source_read_failed", "snapshot_rejected", "timeout", "persistence_failed":
			default:
				return false
			}
			failure += d
		default:
			return false
		}
	}
	if success > c.SuccessfulObservationMicroseconds || failure > c.FailedObservationMicroseconds || unknown > c.UnknownMicroseconds {
		return false
	}
	return out.Truncated || out.Items[len(out.Items)-1].From.Equal(c.Since) && success == c.SuccessfulObservationMicroseconds && failure == c.FailedObservationMicroseconds && unknown == c.UnknownMicroseconds
}

func validCollectionFailureDetails(d api.ObsAlertCollectionFailureDetails, c api.ObsAlertCollectionCoverage) bool {
	if d.Items == nil || d.Limitations == nil || d.TotalFailures != c.Failures || d.Limit != api.ObsAlertCollectionFailureDetailsMax || len(d.Items) != int(min(d.TotalFailures, int64(d.Limit))) || d.Truncated != (d.TotalFailures > int64(len(d.Items))) {
		return false
	}
	for i, item := range d.Items {
		if item.AttemptedAt.IsZero() || item.AttemptedAt.Before(c.Since) || item.AttemptedAt.After(c.AsOf) || i > 0 && !item.AttemptedAt.Before(d.Items[i-1].AttemptedAt) {
			return false
		}
		switch item.Reason {
		case "collection_failed", "source_read_failed", "snapshot_rejected", "timeout", "persistence_failed":
		default:
			return false
		}
		if item.Diagnostics == nil {
			if item.DiagnosticsState != "unknown" {
				return false
			}
		} else if item.DiagnosticsState != "recorded" || item.Reason != "snapshot_rejected" || !api.ValidObsAlertCollectionRejection(item.AttemptedAt, *item.Diagnostics) {
			return false
		}
	}
	return true
}

func writeCollectionFailureDetails(w io.Writer, d api.ObsAlertCollectionFailureDetails) {
	_, _ = fmt.Fprintf(w, "failure_details total_failures=%d limit=%d truncated=%t\n", d.TotalFailures, d.Limit, d.Truncated)
	for _, item := range d.Items {
		_, _ = fmt.Fprintf(w, "attempted_at=%s reason=%s diagnostics=%s\n", item.AttemptedAt.UTC().Format(time.RFC3339Nano), item.Reason, item.DiagnosticsState)
		if item.Diagnostics == nil {
			continue
		}
		r := item.Diagnostics
		_, _ = fmt.Fprintf(w, "  scope=%s reason=%s rules=%d healthy=%d failed=%d stale=%d invalid_clock=%d unavailable=%d invalid=%d invalid_groups=%d issues=%d limit=%d truncated=%t\n", r.Scope, r.Reason, r.Totals.Rules, r.Totals.Healthy, r.Totals.Failed, r.Totals.Stale, r.Totals.InvalidClock, r.Totals.Unavailable, r.Totals.Invalid, r.Totals.InvalidGroups, r.TotalItems, r.Limit, r.Truncated)
		for _, rule := range r.Items {
			at, age := "unknown", "unknown"
			if rule.LastEvaluatedAt != nil {
				at = rule.LastEvaluatedAt.UTC().Format(time.RFC3339Nano)
			}
			if rule.EvaluationAgeSeconds != nil {
				age = fmt.Sprint(*rule.EvaluationAgeSeconds)
			}
			_, _ = fmt.Fprintf(w, "  kind=%s id=%s group_id=%s name=%s state=%s issues=%v last_evaluated_at=%s evaluation_age_seconds=%s has_evaluation_error=%t runbook=%s\n", rule.Kind, rule.ID, rule.GroupID, rule.RuleName, rule.State, rule.Issues, at, age, rule.HasEvaluationError, rule.RunbookURL)
		}
	}
}

func writeCollectionCoverage(w io.Writer, c *api.ObsAlertCollectionCoverage) {
	if c == nil {
		_, _ = fmt.Fprintln(w, "collection_coverage=unknown (not supplied)")
		return
	}
	_, _ = fmt.Fprintf(w, "collection_coverage=%s scope=%s since=%s as_of=%s max_age_seconds=%d successes=%d failures=%d success_observation_microseconds=%d failed_observation_microseconds=%d unknown_microseconds=%d\n", c.State, c.Scope, c.Since.UTC().Format(time.RFC3339Nano), c.AsOf.UTC().Format(time.RFC3339Nano), c.MaxAgeSeconds, c.Successes, c.Failures, c.SuccessfulObservationMicroseconds, c.FailedObservationMicroseconds, c.UnknownMicroseconds)
}
