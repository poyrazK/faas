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

func writeCollectionCoverage(w io.Writer, c *api.ObsAlertCollectionCoverage) {
	if c == nil {
		_, _ = fmt.Fprintln(w, "collection_coverage=unknown (not supplied)")
		return
	}
	_, _ = fmt.Fprintf(w, "collection_coverage=%s scope=%s since=%s as_of=%s max_age_seconds=%d successes=%d failures=%d success_observation_microseconds=%d failed_observation_microseconds=%d unknown_microseconds=%d\n", c.State, c.Scope, c.Since.UTC().Format(time.RFC3339Nano), c.AsOf.UTC().Format(time.RFC3339Nano), c.MaxAgeSeconds, c.Successes, c.Failures, c.SuccessfulObservationMicroseconds, c.FailedObservationMicroseconds, c.UnknownMicroseconds)
}
