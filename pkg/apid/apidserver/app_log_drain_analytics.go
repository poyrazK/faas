package apidserver

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	appLogDrainAnalyticsBucketInterval = time.Hour
)

func appLogDrainAnalyticsWindow(raw string) (string, time.Duration, bool) {
	if raw == "" {
		return "24h", 24 * time.Hour, true
	}
	switch raw {
	case "1h":
		return raw, time.Hour, true
	case "24h":
		return raw, 24 * time.Hour, true
	case "7d":
		return raw, 7 * 24 * time.Hour, true
	case "30d":
		return raw, 30 * 24 * time.Hour, true
	default:
		return "", 0, false
	}
}

func (s *server) getAppLogDrainAnalytics(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	drain, err := s.store.AppLogDrainByID(r.Context(), r.PathValue("id"))
	if err != nil || drain.AppID != app.ID || drain.AccountID != acct.ID {
		s.notFound(w, "log drain not found")
		return
	}
	window, duration, ok := appLogDrainAnalyticsWindow(r.URL.Query().Get("window"))
	if !ok {
		api.WriteProblem(w, api.ErrAppLogDrainInvalid("window must be one of 1h, 24h, 7d, or 30d"))
		return
	}
	to := time.Now().UTC()
	from := to.Add(-duration)
	firstBucket := from.Truncate(appLogDrainAnalyticsBucketInterval)
	samples, err := s.store.ListAppLogDrainDeliveryAnalytics(r.Context(), drain.ID,
		firstBucket, to.Truncate(appLogDrainAnalyticsBucketInterval))
	if err != nil {
		s.log.WarnContext(r.Context(), "read app log drain analytics", slog.String("err", err.Error()))
		api.WriteProblem(w, api.ErrCapacity("could not read log drain analytics"))
		return
	}
	writeJSON(w, http.StatusOK, appLogDrainAnalyticsResponse(samples, drain.ID, window, from, to, drain.CreatedAt))
}

func appLogDrainAnalyticsResponse(samples []state.AppLogDrainDeliveryAnalytics, drainID, window string, from, to time.Time, createdAt ...time.Time) api.AppLogDrainAnalyticsResponse {
	firstBucket := from.Truncate(appLogDrainAnalyticsBucketInterval)
	out := api.AppLogDrainAnalyticsResponse{
		LogDrainID: drainID, Window: window, BucketInterval: "1h",
		From: api.FormatAlertTime(firstBucket), To: api.FormatAlertTime(to),
		RequestedFrom: api.FormatAlertTime(from), WindowAligned: !from.Equal(firstBucket), CoverageGaps: []string{},
		Buckets: make([]api.AppLogDrainAnalyticsBucket, 0, 30),
	}
	complete := true
	out.Complete = &complete
	effectiveFrom, effectiveTo := firstBucket, to
	var latencyNanos, latencySamples int64
	var previous state.AppLogDrainDeliveryAnalytics
	hasPrevious := false
	for _, sample := range samples {
		if sample.BucketStart.Before(firstBucket) {
			previous = sample
			hasPrevious = true
			continue
		}
		baselineKnown := hasPrevious
		if !baselineKnown && len(createdAt) > 0 && !createdAt[0].Before(firstBucket) && !sample.SampledAt.Before(createdAt[0]) {
			baselineKnown = true
		}
		gap := ""
		if !baselineKnown {
			gap = "baseline_missing"
		} else if hasPrevious && sample.BucketStart.Sub(previous.BucketStart) > appLogDrainAnalyticsBucketInterval {
			baselineKnown = false
			gap = "sample_gap"
		}
		if gap != "" {
			complete = false
			if len(out.CoverageGaps) == 0 || out.CoverageGaps[len(out.CoverageGaps)-1] != gap {
				out.CoverageGaps = append(out.CoverageGaps, gap)
			}
		}
		if len(out.Buckets) == 0 && baselineKnown {
			if hasPrevious && !previous.SampledAt.IsZero() {
				effectiveFrom = previous.SampledAt.UTC()
			}
			if !hasPrevious && len(createdAt) > 0 {
				effectiveFrom = createdAt[0].UTC()
			}
		}
		if !sample.SampledAt.IsZero() && !sample.SampledAt.After(to) {
			effectiveTo = sample.SampledAt.UTC()
		}
		latencyNanos += analyticsDelta(sample.DeliveryLatencyNanosTotal, previous.DeliveryLatencyNanosTotal, baselineKnown)
		latencySamples += analyticsDelta(sample.DeliveryLatencySamples, previous.DeliveryLatencySamples, baselineKnown)
		bucket := appLogDrainAnalyticsBucketResponse(sample, previous, baselineKnown)
		out.Buckets = append(out.Buckets, bucket)
		previous = sample
		hasPrevious = true
	}
	out.From = api.FormatAlertTime(effectiveFrom)
	out.To = api.FormatAlertTime(effectiveTo)
	out.WindowAligned = !effectiveFrom.Equal(from) || !effectiveTo.Equal(to)
	if len(out.Buckets) == 0 {
		complete = false
		out.CoverageGaps = append(out.CoverageGaps, "no_samples")
	}
	for _, bucket := range out.Buckets {
		out.Summary.Delivered += bucket.Delivered
		out.Summary.Failed += bucket.Failed
		out.Summary.Dropped += bucket.Dropped
		out.Summary.Retries += bucket.Retries
		out.Summary.DeadLetters += bucket.DeadLetters
	}
	out.Summary.SuccessRate = deliverySuccessRate(out.Summary.Delivered, out.Summary.Failed, out.Summary.Dropped)
	if latencySamples > 0 {
		out.Summary.AverageLatencyMS = float64(latencyNanos) / float64(latencySamples) / float64(time.Millisecond)
	}
	return out
}

func appLogDrainAnalyticsBucketResponse(sample, previous state.AppLogDrainDeliveryAnalytics, hasPrevious bool) api.AppLogDrainAnalyticsBucket {
	delivered := analyticsDelta(sample.DeliveredTotal, previous.DeliveredTotal, hasPrevious)
	failed := analyticsDelta(sample.FailedTotal, previous.FailedTotal, hasPrevious)
	dropped := analyticsDelta(sample.DroppedTotal, previous.DroppedTotal, hasPrevious)
	retries := analyticsDelta(sample.RetriesTotal, previous.RetriesTotal, hasPrevious)
	deadLetters := analyticsDelta(sample.DeadLetterTotal, previous.DeadLetterTotal, hasPrevious)
	latencyNanos := analyticsDelta(sample.DeliveryLatencyNanosTotal, previous.DeliveryLatencyNanosTotal, hasPrevious)
	latencySamples := analyticsDelta(sample.DeliveryLatencySamples, previous.DeliveryLatencySamples, hasPrevious)
	bucket := api.AppLogDrainAnalyticsBucket{
		Start: api.FormatAlertTime(sample.BucketStart), Delivered: delivered,
		Failed: failed, Dropped: dropped, Retries: retries, DeadLetters: deadLetters,
		PendingRecords: sample.PendingRecords, PendingBytes: sample.PendingBytes,
		SuccessRate: deliverySuccessRate(delivered, failed, dropped),
	}
	if latencySamples > 0 {
		bucket.AverageLatencyMS = float64(latencyNanos) / float64(latencySamples) / float64(time.Millisecond)
	}
	return bucket
}

func analyticsDelta(current, previous int64, hasPrevious bool) int64 {
	if !hasPrevious {
		return 0
	}
	if current < previous {
		return current
	}
	return current - previous
}

func deliverySuccessRate(delivered, failed, dropped int64) float64 {
	total := delivered + failed + dropped
	if total == 0 {
		return 0
	}
	return float64(delivered) / float64(total)
}
