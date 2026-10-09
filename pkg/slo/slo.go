// Package slo computes customer-defined SLO budgets (ADR-747): the SLI
// queries against the gateway's Prometheus series, the meterd hourly rollup
// that makes a 30-day window outlive Prometheus retention, and the
// attainment, budget, and burn-rate arithmetic shared by apid and alerts.
package slo

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// PromQL is the instant-query surface the SLO code needs (appmetrics.PromQL
// and *promql.Client satisfy it).
type PromQL interface {
	QueryScalar(ctx context.Context, query string) (float64, error)
}

// queries returns the good and total PromQL expressions for an SLO over a
// range, evaluated at `at` (zero = now). Availability follows ADR-082:
// 5xx responses are bad, 4xx and 3xx are outside the SLI. Latency counts
// requests whose duration fell within the threshold bucket.
func queries(def state.SLO, rng string, at time.Time) (good, total string, err error) {
	if strings.ContainsAny(def.AppID, "\"\n\\") {
		return "", "", fmt.Errorf("slo: invalid app id")
	}
	sel := func(metric, matchers string) string {
		expr := fmt.Sprintf(`sum(increase(%s{app=%q%s}[%s]`, metric, def.AppID, matchers, rng)
		if !at.IsZero() {
			expr += fmt.Sprintf(" @ %d", at.Unix())
		}
		return expr + ")) or vector(0)"
	}
	switch def.SLI {
	case state.SLIAvailability:
		return sel("gateway_requests_total", `,code=~"2.."`), sel("gateway_requests_total", `,code=~"2..|5.."`), nil
	case state.SLILatency:
		le := strconv.FormatFloat(float64(def.LatencyThresholdMS)/1000, 'f', -1, 64)
		return sel("gateway_request_duration_seconds_bucket", fmt.Sprintf(`,le=%q`, le)), sel("gateway_request_duration_seconds_count", ""), nil
	}
	return "", "", fmt.Errorf("slo: unknown SLI %q", def.SLI)
}

// Counts returns good and total requests for the SLO over rng ending at
// `at` (zero = now). increase() extrapolates, so both are rounded and good
// is clamped to total.
func Counts(ctx context.Context, prom PromQL, def state.SLO, rng string, at time.Time) (good, total int64, err error) {
	goodQ, totalQ, err := queries(def, rng, at)
	if err != nil {
		return 0, 0, err
	}
	g, err := prom.QueryScalar(ctx, goodQ)
	if err != nil {
		return 0, 0, fmt.Errorf("slo: good count: %w", err)
	}
	t, err := prom.QueryScalar(ctx, totalQ)
	if err != nil {
		return 0, 0, fmt.Errorf("slo: total count: %w", err)
	}
	total = roundCount(t)
	return min(roundCount(g), total), total, nil
}

func roundCount(v float64) int64 {
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	return int64(math.Round(v))
}

// Objective converts basis points (9990) to a ratio (0.999).
func Objective(bp int) float64 { return float64(bp) / 10000 }

// Attainment is good/total; ok is false when there were no requests.
func Attainment(good, total int64) (float64, bool) {
	if total <= 0 {
		return 0, false
	}
	return float64(good) / float64(total), true
}

// BudgetRemaining is the share of the window's error budget left: 1 when
// nothing failed, 0 when failures exactly used it up, negative when the
// objective is already missed.
func BudgetRemaining(good, total int64, objectiveBP int) (float64, bool) {
	attained, ok := Attainment(good, total)
	if !ok {
		return 1, false
	}
	return 1 - (1-attained)/(1-Objective(objectiveBP)), true
}

// BurnRate is how fast failures spend the budget relative to the rate that
// would use exactly all of it over the window: 1 = on pace, 14.4 = a 30-day
// budget gone in about two days.
func BurnRate(good, total int64, objectiveBP int) (float64, bool) {
	attained, ok := Attainment(good, total)
	if !ok {
		return 0, false
	}
	return (1 - attained) / (1 - Objective(objectiveBP)), true
}
