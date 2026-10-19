package operatorobs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

const NotificationHealthPath = "/v1/admin/obs/notification-health"

var errNotificationNotConfigured = errors.New("notification metrics not configured")

// NotificationHealth keeps each observation independent under one deadline.
// A fresh zero is evidence; a missing counter is never fabricated as zero.
func NotificationHealth(ctx context.Context, client *promql.Client, am *AlertmanagerClient, now time.Time) api.ObsNotificationHealth {
	ctx, cancel := context.WithTimeout(ctx, api.ObsNotificationBudget)
	defer cancel()
	// A slow local Alertmanager must not consume the metrics read's budget.
	sourceResult := make(chan api.ObsNotificationAPIStatus, 1)
	go func() { _, status := am.alerts(ctx, now); sourceResult <- status }()
	out := api.ObsNotificationHealth{GeneratedAt: now.UTC(), WindowSeconds: int(api.ObsNotificationWindow.Seconds()),
		Signals: map[string]api.ObsNotificationSignal{}, Integrations: []api.ObsNotificationIntegration{},
		Limitations: []string{"aggregate_integration_counters", "includes_all_receivers_and_watchdog", "integration_presence_does_not_imply_configuration", "receiver_configuration_unobserved", "extrapolated_counter_increases", "no_per_episode_delivery_receipt", "no_human_receipt_confirmation", "external_watchdog_receipt_unobserved"}}
	queries := []struct {
		name, metric, aggregate string
		counter                 bool
	}{
		{"alertmanager_up", `up{job="alertmanager"}`, "min", false},
		{"alertmanager_config_reload_successful", `alertmanager_config_last_reload_successful{job="alertmanager"}`, "min", false},
		{"prometheus_queue_length", `prometheus_notifications_queue_length{job="prometheus"}`, "max", false},
		{"prometheus_queue_capacity", `prometheus_notifications_queue_capacity{job="prometheus"}`, "min", false},
		{"prometheus_dropped", `prometheus_notifications_dropped_total{job="prometheus"}`, "sum", true},
	}
	for _, q := range queries {
		expr := q.aggregate + "(" + q.metric + ")"
		window := time.Duration(0)
		if q.counter {
			increase := fmt.Sprintf(`increase(%s[%ds])`, q.metric, int(api.ObsNotificationWindow.Seconds()))
			expr = fmt.Sprintf(`sum(%s) and (count(%s) == count(%s))`, increase, increase, q.metric)
			window = api.ObsNotificationWindow
		}
		values, err := notificationVector(ctx, client, expr)
		observed, obsErr := notificationVector(ctx, client, counterObservationQuery(q.metric))
		out.Signals[q.name] = notificationSignal(values, err, observed, obsErr, window, now)
	}
	attempts, attemptsSummary := notificationIntegrations(ctx, client, "alertmanager_notifications_total", now)
	failures, failuresSummary := notificationIntegrations(ctx, client, "alertmanager_notifications_failed_total", now)
	out.Signals["alertmanager_attempts"], out.Signals["alertmanager_failures"] = attemptsSummary, failuresSummary
	names := map[string]bool{}
	for name := range attempts {
		names[name] = true
	}
	for name := range failures {
		names[name] = true
	}
	for name := range names {
		a, aok := attempts[name]
		f, fok := failures[name]
		if !aok {
			a = missingIntegrationSignal(attemptsSummary)
		}
		if !fok {
			f = missingIntegrationSignal(failuresSummary)
		}
		out.Integrations = append(out.Integrations, api.ObsNotificationIntegration{Integration: name, Attempts: a, Failures: f})
	}
	sort.Slice(out.Integrations, func(i, j int) bool { return out.Integrations[i].Integration < out.Integrations[j].Integration })
	out.AlertmanagerAPI = <-sourceResult
	out.State = notificationOverallState(out)
	return out
}

func notificationVector(ctx context.Context, client *promql.Client, query string) ([]promql.VectorSample, error) {
	if client == nil || client.BaseURL() == "" {
		return nil, errNotificationNotConfigured
	}
	return client.QueryVectorStrict(ctx, query)
}

func notificationSignal(values []promql.VectorSample, err error, observations []promql.VectorSample, obsErr error, window time.Duration, now time.Time) api.ObsNotificationSignal {
	out := api.ObsNotificationSignal{State: api.ObsHealthSignalUnavailable, Reason: "query_failed", WindowSeconds: int(window.Seconds()), MaxAgeSeconds: int(api.ObsHealthFreshnessMaxAge.Seconds())}
	if errors.Is(err, errNotificationNotConfigured) || errors.Is(obsErr, errNotificationNotConfigured) {
		out.Reason = "not_configured"
		return out
	}
	if err != nil || obsErr != nil {
		return out
	}
	if len(values) > 1 || len(observations) > 1 {
		out.Reason = "invalid_series"
		return out
	}
	if len(observations) == 0 {
		out.State, out.Reason = api.ObsHealthSignalNoSamples, "missing_observation"
		return out
	}
	observed := observations[0].Value
	if observed <= 0 || math.IsNaN(observed) || math.IsInf(observed, 0) || observed > float64(now.Add(api.ObsHealthClockSkewTolerance).UnixMilli())/1000 {
		out.Reason = "invalid_observation"
		return out
	}
	timestamp := time.UnixMilli(int64(math.Round(observed * 1000))).UTC()
	out.ObservedAt = &timestamp
	if timestamp.Before(now.Add(-api.ObsHealthFreshnessMaxAge)) {
		out.State, out.Reason = api.ObsHealthSignalStale, "observation_stale"
		return out
	}
	if len(values) == 0 {
		out.State, out.Reason = api.ObsHealthSignalNoSamples, "insufficient_samples"
		return out
	}
	value := values[0].Value
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		out.Reason = "invalid_value"
		return out
	}
	out.State, out.Reason, out.Value = api.ObsHealthSignalHealthy, "", &value
	return out
}

func missingIntegrationSignal(summary api.ObsNotificationSignal) api.ObsNotificationSignal {
	summary.Value, summary.ObservedAt = nil, nil
	if summary.State == api.ObsHealthSignalHealthy {
		summary.State, summary.Reason = api.ObsHealthSignalNoSamples, "missing_integration_counter"
	}
	return summary
}

func notificationOverallState(out api.ObsNotificationHealth) string {
	state := "healthy"
	if out.AlertmanagerAPI.State != "healthy" {
		state = "partial"
	}
	for _, signal := range out.Signals {
		if signal.State != api.ObsHealthSignalHealthy {
			state = "partial"
		}
	}
	for _, integration := range out.Integrations {
		if integration.Attempts.State != api.ObsHealthSignalHealthy || integration.Failures.State != api.ObsHealthSignalHealthy {
			state = "partial"
		}
		if integration.Failures.Value != nil && *integration.Failures.Value > 0 {
			return "degraded"
		}
	}
	value := func(name string) *float64 { return out.Signals[name].Value }
	for _, name := range []string{"alertmanager_up", "alertmanager_config_reload_successful"} {
		if v := value(name); v != nil && *v != 1 {
			return "degraded"
		}
	}
	for _, name := range []string{"prometheus_dropped", "alertmanager_failures"} {
		if v := value(name); v != nil && *v > 0 {
			return "degraded"
		}
	}
	length, capacity := value("prometheus_queue_length"), value("prometheus_queue_capacity")
	if capacity != nil && *capacity == 0 {
		return "degraded"
	}
	if length != nil && capacity != nil && *length / *capacity > 0.8 {
		return "degraded"
	}
	return state
}
