package appmetrics

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/onebox-faas/faas/pkg/promql"
)

// APIAvailabilitySLO is the ADR-082 API availability target. The
// complement is the monthly error budget used by burn-rate alerts.
const APIAvailabilitySLO = 0.995

const (
	SLOBurnRateShortWindow = "1h"
	SLOBurnRateLongWindow  = "6h"
	SLOBurnRateShortLimit  = 14.4
	SLOBurnRateLongLimit   = 6.0
	// SLOBurnRateMinRequests prevents one isolated server error on an idle
	// application from becoming a multi-window incident. Raw 5xx counts and
	// error-rate telemetry remain visible below this floor.
	SLOBurnRateMinRequests = 20
)

// FetchSLOBurnRate evaluates the customer-facing API availability burn-rate
// signal using the Google SRE multi-window shape. The returned value is the
// short-window burn rate, with the long-window burn rate rescaled to the
// same 14.4x threshold. A value above 14.4 therefore means both conditions
// hold: 1h > 14.4x and 6h > 6x of the 99.5% availability error budget.
//
// The effective value is deliberately fail-closed when either PromQL query
// is unavailable. An app with no traffic produces no Prometheus sample and
// is treated as degraded rather than as a confirmed SLO breach.
func FetchSLOBurnRate(ctx context.Context, fetcher PromQL, log *slog.Logger, appID string) (float64, string) {
	if log == nil {
		log = slog.Default()
	}
	if fetcher == nil {
		return 0, SourceDegradedPrefix + "prometheus not configured"
	}
	if client, ok := fetcher.(*promql.Client); ok && client == nil {
		return 0, SourceDegradedPrefix + "prometheus not configured"
	}
	if strings.ContainsAny(appID, "\"\n\\") {
		return 0, SourceDegradedPrefix + "invalid app id"
	}

	errorBudget := 1 - APIAvailabilitySLO
	shortQ := fmt.Sprintf(
		`(sum(rate(gateway_requests_total{app=%q,code=~"5.."}[%s])) / sum(rate(gateway_requests_total{app=%q,code=~"2..|5.."}[%s])) / %g and sum(increase(gateway_requests_total{app=%q,code=~"2..|5.."}[%s])) >= %d) or vector(0)`,
		appID, SLOBurnRateShortWindow, appID, SLOBurnRateShortWindow, errorBudget,
		appID, SLOBurnRateShortWindow, SLOBurnRateMinRequests)
	short, err := fetcher.QueryScalar(ctx, shortQ)
	if err != nil {
		return degradedBurnRateFromErr(0, err, log, SLOBurnRateShortWindow)
	}

	longQ := fmt.Sprintf(
		`(sum(rate(gateway_requests_total{app=%q,code=~"5.."}[%s])) / sum(rate(gateway_requests_total{app=%q,code=~"2..|5.."}[%s])) / %g and sum(increase(gateway_requests_total{app=%q,code=~"2..|5.."}[%s])) >= %d) or vector(0)`,
		appID, SLOBurnRateLongWindow, appID, SLOBurnRateLongWindow, errorBudget,
		appID, SLOBurnRateLongWindow, SLOBurnRateMinRequests)
	long, err := fetcher.QueryScalar(ctx, longQ)
	if err != nil {
		return degradedBurnRateFromErr(0, err, log, SLOBurnRateLongWindow)
	}

	short = SafeFloat(short)
	long = SafeFloat(long)
	// Scale the long-window value so one comparison against the short
	// threshold preserves the canonical 14.4x / 6x Google SRE shape.
	effective := math.Min(short, long*SLOBurnRateShortLimit/SLOBurnRateLongLimit)
	return SafeFloat(effective), SourcePrometheus
}

func degradedBurnRateFromErr(value float64, err error, log *slog.Logger, window string) (float64, string) {
	msg := strings.ReplaceAll(err.Error(), "\r", "")
	msg = strings.ReplaceAll(msg, "\n", "")
	log.Warn("appmetrics: SLO burn-rate query failed", "window", window, "err", msg)
	return value, SourceDegradedPrefix + msg
}
