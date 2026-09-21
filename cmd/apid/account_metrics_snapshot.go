package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/promql"
)

// accountMetricsSnapshot is one bounded Prometheus projection for the apps
// owned by an account. Request totals and error-rate inputs deliberately come
// from the same grouped counter query: the previous implementation scanned
// the 24h request counters repeatedly for totals, error rate, cold-boot rate,
// and each latency percentile. Large beta accounts could exhaust the 3s
// Prometheus budget even while Prometheus itself was healthy.
type accountMetricsSnapshot struct {
	requestsByApp       map[string]float64
	eligibleByApp       map[string]float64
	errorsByApp         map[string]float64
	coldBootsByApp      map[string]float64
	throttledByApp      map[string]float64
	latencyBucketsByApp map[string]map[string]float64
	wakeP95MS           float64

	requestsErr  error
	coldBootsErr error
	throttledErr error
	latencyErr   error
	wakeErr      error
}

// fetchAccountMetricsSnapshot evaluates independent metric families in
// parallel. Every tenant-bearing selector is constrained to the closed app-ID
// set before Prometheus evaluates it; the allowed map is a second boundary on
// returned labels. includeThrottled is used by the account SLO panel;
// includeFleetWake is reserved for the legacy apps-metrics response, whose
// contract explicitly labels wake p95 as a fleet figure.
func fetchAccountMetricsSnapshot(ctx context.Context, client *promql.Client, appIDs []string, window string, includeThrottled, includeFleetWake bool) accountMetricsSnapshot {
	out := accountMetricsSnapshot{}
	if client == nil || len(appIDs) == 0 {
		return out
	}

	allowed := make(map[string]struct{}, len(appIDs))
	for _, id := range appIDs {
		allowed[id] = struct{}{}
	}
	matcher := appmetrics.AppIDMatcher(appIDs)

	var wg sync.WaitGroup
	taskCount := 3
	if includeThrottled {
		taskCount++
	}
	if includeFleetWake {
		taskCount++
	}
	wg.Add(taskCount)

	go func() {
		defer wg.Done()
		rows, err := client.QueryVector(ctx, fmt.Sprintf(
			`sum by (app, class)(increase(gateway_request_duration_seconds_count{%s}[%s]))`,
			matcher, window))
		if err != nil {
			out.requestsErr = err
			return
		}
		out.requestsByApp = make(map[string]float64, len(appIDs))
		out.eligibleByApp = make(map[string]float64, len(appIDs))
		out.errorsByApp = make(map[string]float64, len(appIDs))
		for _, row := range rows {
			appID := row.Labels["app"]
			if _, ok := allowed[appID]; !ok {
				continue
			}
			out.requestsByApp[appID] += row.Value
			switch row.Labels["class"] {
			case "2xx":
				out.eligibleByApp[appID] += row.Value
			case "5xx":
				out.eligibleByApp[appID] += row.Value
				out.errorsByApp[appID] += row.Value
			}
		}
	}()

	go func() {
		defer wg.Done()
		out.coldBootsByApp, out.coldBootsErr = client.QueryMap(ctx, fmt.Sprintf(
			`sum by (app)(increase(gateway_cold_boot_total{%s}[%s]))`,
			matcher, window))
	}()

	go func() {
		defer wg.Done()
		out.latencyBucketsByApp, out.latencyErr = client.QueryBuckets(ctx, fmt.Sprintf(
			`sum by (app, le)(increase(gateway_request_duration_seconds_bucket{%s,class="2xx"}[%s]))`,
			matcher, window))
	}()

	if includeThrottled {
		go func() {
			defer wg.Done()
			out.throttledByApp, out.throttledErr = client.QueryMap(ctx, fmt.Sprintf(
				`sum by (app)(increase(gateway_rate_limited_total{%s}[%s]))`,
				matcher, window))
		}()
	}

	if includeFleetWake {
		go func() {
			defer wg.Done()
			wakeQ := appmetrics.HistogramQuantileMSQuery(
				0.95,
				fmt.Sprintf(`sum by (le)(rate(gateway_wake_latency_seconds_bucket[%s]))`, window),
				fmt.Sprintf(`sum(rate(gateway_wake_latency_seconds_count[%s]))`, window))
			out.wakeP95MS, out.wakeErr = client.QueryScalar(ctx, wakeQ)
		}()
	}

	wg.Wait()
	return out
}

func percentOf(numerator, denominator float64) float64 {
	if numerator <= 0 || denominator <= 0 {
		return 0
	}
	return numerator / denominator * 100
}

func aggregateMetricValues(values map[string]float64, appIDs []string) float64 {
	var total float64
	for _, appID := range appIDs {
		total += values[appID]
	}
	return total
}

func aggregateLatencyBuckets(values map[string]map[string]float64, appIDs []string) map[string]float64 {
	out := make(map[string]float64)
	for _, appID := range appIDs {
		for upperBound, value := range values[appID] {
			out[upperBound] += value
		}
	}
	return out
}
