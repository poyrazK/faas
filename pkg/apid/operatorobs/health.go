package operatorobs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

const (
	healthAuditWrites   = "audit_log_write_total_5m"
	healthAuditFailures = "audit_log_write_failures_5m"
	healthAlerts        = "alerts_firing"
)

// PrometheusHealthSnapshot retains the numeric compatibility fields while
// exposing each signal's evidence independently. Non-healthy values are zero
// fallbacks; a partial failure does not erase another signal's valid value.
type PrometheusHealthSnapshot struct {
	Available     bool
	AuditWrites   int64
	AuditFailures int64
	AlertsFiring  int64
	Signals       map[string]api.ObsHealthSignal
}

type healthQuery struct {
	name, value, observedAt string
	window                  time.Duration
}

func healthQueries() []healthQuery {
	return []healthQuery{
		{healthAuditWrites, `sum(increase(apid_audit_log_write_total[5m]))`, counterObservationQuery("apid_audit_log_write_total"), api.ObsHealthWindow},
		{healthAuditFailures, `sum(increase(apid_audit_log_write_failures_total[5m]))`, counterObservationQuery("apid_audit_log_write_failures_total"), api.ObsHealthWindow},
		{healthAlerts, `count(ALERTS{alertstate="firing",alertname!="FaasWatchdog",severity!="info"}) or vector(0)`,
			fmt.Sprintf(`min(last_over_time(prometheus_rule_group_last_evaluation_timestamp_seconds{rule_group=~".*faas[.]rules[.]yml;.*"}[%ds]))`, int(api.ObsHealthObservationLookback.Seconds())), 0},
	}
}

// timestamp() is inside the subquery: its values remain the original sample
// times even if the outer range query evaluates now. min over the latest
// observation per series prevents a fresh series from masking an older one.
func counterObservationQuery(metric string) string {
	return fmt.Sprintf(`min(max_over_time(timestamp(%s)[%ds:%ds]))`, metric,
		int(api.ObsHealthObservationLookback.Seconds()), int(api.ObsHealthObservationStep.Seconds()))
}

// PrometheusHealth reads the three metrics under a shared deadline. It keeps
// no last-good cache, so outages cannot silently reuse another request's data.
func PrometheusHealth(ctx context.Context, client *promql.Client, now time.Time) PrometheusHealthSnapshot {
	out := PrometheusHealthSnapshot{Signals: make(map[string]api.ObsHealthSignal, 3), Available: true}
	ctx, cancel := context.WithTimeout(ctx, api.ObsHealthPrometheusBudget)
	defer cancel()
	for _, query := range healthQueries() {
		signal := api.ObsHealthSignal{State: api.ObsHealthSignalUnavailable, Reason: "not_configured",
			WindowSeconds: int(query.window.Seconds()), MaxAgeSeconds: int(api.ObsHealthFreshnessMaxAge.Seconds())}
		var value int64
		if client != nil && client.BaseURL() != "" {
			value, signal = readHealthSignal(ctx, client, query, signal, now)
		}
		out.Signals[query.name] = signal
		out.Available = out.Available && signal.State == api.ObsHealthSignalHealthy
		switch query.name {
		case healthAuditWrites:
			out.AuditWrites = value
		case healthAuditFailures:
			out.AuditFailures = value
		case healthAlerts:
			out.AlertsFiring = value
		}
	}
	return out
}

func readHealthSignal(ctx context.Context, client *promql.Client, query healthQuery, signal api.ObsHealthSignal, now time.Time) (int64, api.ObsHealthSignal) {
	value, valueErr := client.QueryScalar(ctx, query.value)
	observed, observedErr := client.QueryScalar(ctx, query.observedAt)
	signal.State, signal.Reason = api.ObsHealthSignalUnavailable, "query_failed"
	if (valueErr != nil && !errors.Is(valueErr, promql.ErrNoData)) || (observedErr != nil && !errors.Is(observedErr, promql.ErrNoData)) {
		return 0, signal
	}
	if errors.Is(observedErr, promql.ErrNoData) || observed == 0 {
		signal.State, signal.Reason = api.ObsHealthSignalNoSamples, "missing_observation"
		return 0, signal
	}
	if observed < 0 || math.IsNaN(observed) || math.IsInf(observed, 0) {
		signal.Reason = "invalid_observation"
		return 0, signal
	}
	if observed > float64(now.Add(api.ObsHealthClockSkewTolerance).UnixMilli())/1000 {
		signal.Reason = "observation_in_future"
		return 0, signal
	}
	observedAt := time.UnixMilli(int64(math.Round(observed * 1000))).UTC()
	signal.ObservedAt = &observedAt
	if observedAt.Before(now.Add(-api.ObsHealthFreshnessMaxAge)) {
		signal.State, signal.Reason = api.ObsHealthSignalStale, "observation_stale"
		return 0, signal
	}
	if errors.Is(valueErr, promql.ErrNoData) {
		signal.State, signal.Reason = api.ObsHealthSignalNoSamples, "insufficient_samples"
		return 0, signal
	}
	// increase() can extrapolate fractional counts; retain the existing
	// truncation contract while rejecting negative/overflowing fallback values.
	if value < 0 || value >= math.Ldexp(1, 63) || math.IsNaN(value) || math.IsInf(value, 0) {
		signal.Reason = "invalid_value"
		return 0, signal
	}
	signal.State, signal.Reason = api.ObsHealthSignalHealthy, ""
	return int64(value), signal
}
