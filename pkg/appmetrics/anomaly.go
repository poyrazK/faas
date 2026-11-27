package appmetrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// Allow four scrape intervals for a fresh gateway sample.
const anomalyScrapeMaxAgeSeconds = 60

const anomalyCoverageMissing = -1

const anomalyDay = 24 * time.Hour

type anomalyFetcher interface {
	QueryVectorStrictAt(context.Context, string, time.Time) ([]promql.VectorSample, error)
	QueryRangeStrict(context.Context, string, string, string, string) ([]promql.QueryRangeSeries, error)
}

// FetchAnomalyAlertMetric evaluates the metric and its sample guard at the
// same instant so both queries cover exactly the same window.
func FetchAnomalyAlertMetric(ctx context.Context, fetcher PromQL, log *slog.Logger, appID, scope, rng, metric string, at time.Time) (float64, string, api.AlertAnomalyEvaluation) {
	var evidence api.AlertAnomalyEvaluation
	if problem := validateAlertMetricInput(fetcher, appID, rng); problem != "" {
		return 0, problem, evidence
	}
	client, ok := fetcher.(anomalyFetcher)
	if !ok {
		return 0, SourceDegradedPrefix + "anomaly query client unavailable", evidence
	}
	if !supportedAnomalyMetric(metric, scope) {
		return 0, SourceDegradedPrefix + "unsupported anomaly alert metric", evidence
	}
	read := func(query string) (float64, error) {
		rows, err := client.QueryVectorStrictAt(ctx, query, at)
		if err != nil {
			return 0, err
		}
		if len(rows) != 1 || len(rows[0].Labels) != 0 {
			return 0, fmt.Errorf("expected one unlabeled anomaly sample")
		}
		return rows[0].Value, nil
	}
	series := requestSeriesForScope(appID, scope)
	samples, err := read(anomalySamplesQuery(series, rng, metric))
	if err != nil {
		return 0, alertMetricQueryFailure(log, appID, metric, err), evidence
	}
	if samples == anomalyCoverageMissing {
		evidence.Reason = api.AlertAnomalyTelemetryMissing
		return 0, SourceDegradedPrefix + "request telemetry missing or stale", evidence
	}
	if !validHealthCount(samples) {
		return 0, SourceDegradedPrefix + "invalid eligible request count", evidence
	}
	if metric == "request_count" {
		return float64(int64(samples)), SourcePrometheus, evidence
	}
	if samples < api.AnomalyAlertMinRequests {
		evidence = api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyInsufficientTraffic, Count: int64(samples), RequiredCount: api.AnomalyAlertMinRequests}
		return 0, SourceInsufficientPrefix + "too few eligible requests", evidence
	}
	query, normalize := anomalyValueQuery(series, appID, scope, rng, metric)
	value, err := read(query)
	if err != nil {
		return 0, alertMetricQueryFailure(log, appID, metric, err), evidence
	}
	return normalize(value), SourcePrometheus, evidence
}

// FetchAnomalyAlertHistory reads the seven aligned historical windows in a
// range query per expression. Missing coverage and sparse windows are skipped.
func FetchAnomalyAlertHistory(ctx context.Context, fetcher PromQL, log *slog.Logger, appID, scope, rng, metric string, at time.Time) ([]float64, int, string) {
	if problem := validateAlertMetricInput(fetcher, appID, rng); problem != "" {
		return nil, 0, problem
	}
	client, ok := fetcher.(anomalyFetcher)
	if !ok {
		return nil, 0, SourceDegradedPrefix + "anomaly query client unavailable"
	}
	if !supportedAnomalyMetric(metric, scope) {
		return nil, 0, SourceDegradedPrefix + "unsupported anomaly alert metric"
	}
	start, end := at.Add(-api.AnomalyBaselineDays*anomalyDay), at.Add(-anomalyDay)
	read := func(query string) ([]promql.QueryRangeSample, error) {
		rows, err := client.QueryRangeStrict(ctx, query, strconv.FormatInt(start.Unix(), 10), strconv.FormatInt(end.Unix(), 10), "1d")
		if err != nil {
			return nil, err
		}
		return alignedAnomalySamples(rows, start, anomalyDay, api.AnomalyBaselineDays)
	}
	series := requestSeriesForScope(appID, scope)
	samples, err := read(anomalySamplesQuery(series, rng, metric))
	if err != nil {
		return nil, 0, alertMetricQueryFailure(log, appID, metric, err)
	}
	var eligible []int
	var values []float64
	for i, sample := range samples {
		if sample.Value == anomalyCoverageMissing {
			continue
		}
		if !validHealthCount(sample.Value) {
			return nil, 0, SourceDegradedPrefix + "invalid historical eligible request count"
		}
		if metric == "request_count" {
			if sample.Value >= 1 {
				values = append(values, float64(int64(sample.Value)))
			}
		} else if sample.Value >= api.AnomalyAlertMinRequests {
			eligible = append(eligible, i)
		}
	}
	if metric == "request_count" {
		return values, len(values), SourcePrometheus
	}
	if len(eligible) < api.AnomalyBaselineMinDays {
		return nil, len(eligible), SourcePrometheus
	}
	query, normalize := anomalyValueQuery(series, appID, scope, rng, metric)
	history, err := read(query)
	if err != nil {
		return nil, 0, alertMetricQueryFailure(log, appID, metric, err)
	}
	for _, i := range eligible {
		values = append(values, normalize(history[i].Value))
	}
	return values, len(values), SourcePrometheus
}

// AnomalyPreviewSample keeps the metric and its coverage at one checkpoint.
type AnomalyPreviewSample struct {
	At       time.Time
	Value    float64
	Requests int64
	Covered  bool
}

func supportedAnomalyMetric(metric, scope string) bool {
	_, ok := api.AnomalyAlertMetrics[metric]
	return ok && (scope == "" || api.SupportsEnvironmentAnomalyMetric(metric))
}

func anomalyValueQuery(series requestSeries, appID, scope, window, metric string) (string, func(float64) float64) {
	if scope == "" {
		return appWideAnomalyQuery(appID, window, metric)
	}
	if metric == "error_rate_pct" {
		return series.errorRate(window), SafePercent
	}
	return series.latency(window, .95), SafeFloat
}

// FetchAnomalySamples uses the live metric and coverage expressions at
// aligned checkpoints. count includes the sample at first.
func FetchAnomalySamples(ctx context.Context, fetcher PromQL, appID, scope, window, metric string, first time.Time, step time.Duration, count int) ([]AnomalyPreviewSample, error) {
	if !supportedAnomalyMetric(metric, scope) {
		return nil, fmt.Errorf("unsupported anomaly metric %q", metric)
	}
	read := func(query string) ([]promql.QueryRangeSample, error) {
		return fetchAnomalyPreviewRange(ctx, fetcher, appID, window, query, first, step, count)
	}
	series := requestSeriesForScope(appID, scope)
	samples, err := read(anomalySamplesQuery(series, window, metric))
	if err != nil {
		return nil, err
	}
	result := make([]AnomalyPreviewSample, len(samples))
	hasEligibleRequests := false
	for i, sample := range samples {
		result[i].At = time.Unix(sample.Timestamp, 0).UTC()
		if sample.Value == anomalyCoverageMissing {
			continue
		}
		if !validHealthCount(sample.Value) {
			return nil, fmt.Errorf("invalid anomaly request count")
		}
		result[i].Requests = int64(sample.Value)
		result[i].Covered = true
		if metric == "request_count" {
			result[i].Value = float64(result[i].Requests)
		}
		hasEligibleRequests = hasEligibleRequests || sample.Value >= api.AnomalyAlertMinRequests
	}
	if metric != "request_count" && hasEligibleRequests {
		query, normalize := anomalyValueQuery(series, appID, scope, window, metric)
		values, err := read(query)
		if err != nil {
			return nil, err
		}
		for i, value := range values {
			result[i].Value = normalize(value.Value)
		}
	}
	return result, nil
}

// FetchAnomalyPreviewFailedRequests reads 5xx responses and capacity refusals
// at the same checkpoints as the error-rate and eligible-request ranges.
func FetchAnomalyPreviewFailedRequests(ctx context.Context, fetcher PromQL, appID, scope, window string, first time.Time, step time.Duration, count int) ([]int64, error) {
	query := requestSeriesForScope(appID, scope).requestsWithRefusals("increase", window, failedRequestClass) + " or vector(0)"
	samples, err := fetchAnomalyPreviewRange(ctx, fetcher, appID, window, query, first, step, count)
	if err != nil {
		return nil, err
	}
	failures := make([]int64, len(samples))
	for i, sample := range samples {
		if !validHealthCount(sample.Value) {
			return nil, fmt.Errorf("invalid anomaly failed request count")
		}
		failures[i] = int64(sample.Value)
	}
	return failures, nil
}

func fetchAnomalyPreviewRange(ctx context.Context, fetcher PromQL, appID, window, query string, first time.Time, step time.Duration, count int) ([]promql.QueryRangeSample, error) {
	if problem := validateAlertMetricInput(fetcher, appID, window); problem != "" {
		return nil, errors.New(problem)
	}
	client, ok := fetcher.(anomalyFetcher)
	if !ok {
		return nil, fmt.Errorf("anomaly query client unavailable")
	}
	last := first.Add(time.Duration(count-1) * step)
	rows, err := client.QueryRangeStrict(ctx, query, strconv.FormatInt(first.Unix(), 10), strconv.FormatInt(last.Unix(), 10), strconv.FormatInt(int64(step/time.Second), 10))
	if err != nil {
		return nil, err
	}
	return alignedAnomalySamples(rows, first, step, count)
}

func alignedAnomalySamples(rows []promql.QueryRangeSeries, first time.Time, step time.Duration, count int) ([]promql.QueryRangeSample, error) {
	if len(rows) != 1 || len(rows[0].Metric) != 0 || len(rows[0].Values) != count {
		return nil, fmt.Errorf("expected one anomaly series with %d samples", count)
	}
	for i, sample := range rows[0].Values {
		if sample.Timestamp != first.Add(time.Duration(i)*step).Unix() {
			return nil, fmt.Errorf("misaligned anomaly history sample")
		}
	}
	return rows[0].Values, nil
}

func anomalySamplesQuery(series requestSeries, window, metric string) string {
	appCounter := series.counter("")
	sampleCount := fmt.Sprintf(`sum(increase(%s[%s]))`, appCounter, window)
	switch metric {
	case "error_rate_pct":
		sampleCount = series.requestsWithRefusals("increase", window, eligibleRequestClasses)
		appCounter = fmt.Sprintf(`{__name__=~"%s_count|%s",%s}`, series.duration, series.capacityRefusals, series.labels)
	case "latency_p95_ms", "latency_p99_ms":
		sampleCount = fmt.Sprintf(`sum(increase(%s[%s]))`, series.counter(successfulRequestClass), window)
	}

	// Each series seen in the window must have at least two scrapes and a
	// fresh sample at the window's end. An empty set cannot certify zero.
	scrapes := fmt.Sprintf(`count_over_time(%s[%s])`, appCounter, window)
	latest := fmt.Sprintf(`timestamp(%s) >= time() - %d`, appCounter, anomalyScrapeMaxAgeSeconds)
	covered := fmt.Sprintf(`count((%s >= %d) and (%s)) == count(%s)`, scrapes, api.AppHealthCounterMinSamples, latest, scrapes)

	// Eligible traffic can be zero when the app counters are healthy, such
	// as when only 4xx responses were observed. -1 encodes missing coverage.
	return fmt.Sprintf(`((%s or vector(0)) and (%s)) or vector(%d)`, sampleCount, covered, anomalyCoverageMissing)
}
