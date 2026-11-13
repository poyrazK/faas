package operatorobs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

const (
	receiverExpected = "faas_notification_receiver_expected"
	receiverUp       = "faas_notification_receiver_collector_up"
	receiverRun      = "faas_notification_receiver_collector_last_run_timestamp_seconds"
	receiverRequests = "faas_notification_receiver_requests_total"
	receiverFailures = "faas_notification_receiver_failures_total"
	receiverSuccess  = "faas_notification_receiver_last_success_observed_timestamp_seconds"
	receiverMetric   = "evidence_metric"
)

// NotificationReceiverNames contains roles, never arbitrary configured names.
func NotificationReceiverNames() []string {
	return []string{"faas-heartbeat", "faas-page", "faas-warn"}
}

// NotificationReceiverLimitations describes the scope of this read-only view.
func NotificationReceiverLimitations() []string {
	return []string{"deployment_intent_not_active_configuration", "receiver_groups_aggregate_destinations", "success_clock_is_confirmation_time", "extrapolated_counter_increases", "no_per_episode_delivery_receipt", "no_human_receipt_confirmation", "external_watchdog_receipt_unobserved"}
}

// NotificationReceiverReasons bounds diagnostics without exposing source data.
func NotificationReceiverReasons() []string {
	return []string{"", "query_failed", "not_configured", "invalid_series", "missing_observation", "invalid_observation", "observation_stale", "insufficient_samples", "invalid_value", "collector_missing", "ambiguous_collector", "invalid_collector_value", "node_scrape_unavailable", "invalid_collector_clock", "inventory_missing", "invalid_inventory", "inventory_node_unavailable", "inventory_source_mismatch", "invalid_receiver_count", "collector_unavailable", "invalid_counter", "inconsistent_counters", "success_not_observed", "invalid_success_clock", "evidence_unavailable", "incomplete_evidence", "receiver_fault", "requests_failing", "inventory_unavailable", "unexpected_group", "telemetry_missing"}
}

type notificationReceiverPair struct{ receiver, integration string }
type notificationReceiverKey struct {
	metric, target string
	pair           notificationReceiverPair
}
type notificationReceiverQuery struct {
	rows []promql.VectorSample
	err  error
}
type notificationReceiverRead struct {
	values, times, requests, failures map[notificationReceiverKey]float64
	requestErr, failureErr            error
}

// Copy the metric name before timestamp/increase removes __name__. All reads
// share one evaluation time; private scrape identity is used only for joins.
func notificationReceiverQueries() []string {
	selector := `{__name__=~"up|faas_notification_receiver_(expected|collector_up|collector_last_run_timestamp_seconds|requests_total|failures_total|last_success_observed_timestamp_seconds)",job="node"}`
	values := `label_replace(` + selector + `,"evidence_metric","$1","__name__","(.+)")`
	increase := func(metric string) string {
		return fmt.Sprintf(`label_replace(increase(%s{job="node"}[%ds]),"evidence_metric",%q,"__name__",".*")`, metric, int(api.ObsNotificationWindow.Seconds()), metric)
	}
	return []string{values, "timestamp(" + values + ")", increase(receiverRequests), increase(receiverFailures)}
}

func notificationReceiverHealth(ctx context.Context, client *promql.Client, now time.Time) api.ObsNotificationReceiverHealth {
	out := api.ObsNotificationReceiverHealth{State: "partial", Reason: "evidence_unavailable", Groups: []api.ObsNotificationReceiver{}, Limitations: NotificationReceiverLimitations()}
	queries := notificationReceiverQueries()
	results := make([]notificationReceiverQuery, len(queries))
	var wg sync.WaitGroup
	for i, query := range queries {
		wg.Go(func() {
			if client == nil || client.BaseURL() == "" {
				results[i].err = errNotificationNotConfigured
				return
			}
			results[i].rows, results[i].err = client.QueryVectorStrictAt(ctx, query, now)
		})
	}
	wg.Wait()
	read := notificationReceiverRead{}
	var valueErr, timeErr error
	read.values, valueErr = notificationReceiverIndex(results[0], "")
	read.times, timeErr = notificationReceiverIndex(results[1], "")
	if valueErr != nil || timeErr != nil {
		out.Collector = notificationSignal(nil, valueErr, nil, timeErr, 0, now)
		out.Inventory = out.Collector
		return out
	}
	read.requests, read.requestErr = notificationReceiverIndex(results[2], receiverRequests)
	read.failures, read.failureErr = notificationReceiverIndex(results[3], receiverFailures)
	out.Collector, out.Inventory, out.Groups = read.assess(now)
	out.State, out.Reason = notificationReceiverOverall(out)
	return out
}

func notificationReceiverIndex(result notificationReceiverQuery, only string) (map[notificationReceiverKey]float64, error) {
	if result.err != nil {
		return nil, result.err
	}
	out := map[notificationReceiverKey]float64{}
	pairs := map[notificationReceiverPair]bool{}
	for _, row := range result.rows {
		key, valid := notificationReceiverIdentity(row.Labels)
		if !valid || only != "" && key.metric != only {
			return nil, errors.New("invalid receiver evidence")
		}
		if _, exists := out[key]; exists {
			return nil, errors.New("duplicate receiver evidence")
		}
		if key.pair.receiver != "" {
			pairs[key.pair] = true
		}
		out[key] = row.Value
	}
	if len(pairs) > api.ObsNotificationIntegrationsMax {
		return nil, errors.New("receiver evidence bound exceeded")
	}
	return out, nil
}

func notificationReceiverIdentity(labels map[string]string) (notificationReceiverKey, bool) {
	key := notificationReceiverKey{metric: labels[receiverMetric]}
	if labels["job"] != "node" || labels["instance"] == "" || labels["__name__"] != "" && labels["__name__"] != key.metric {
		return key, false
	}
	switch key.metric {
	case receiverExpected, receiverRequests, receiverFailures, receiverSuccess:
		key.pair = notificationReceiverPair{labels["receiver"], labels["integration"]}
		if !slices.Contains(NotificationReceiverNames(), key.pair.receiver) || !knownNotificationIntegration(key.pair.integration) {
			return key, false
		}
	case "up", receiverUp, receiverRun:
		if labels["receiver"] != "" || labels["integration"] != "" {
			return key, false
		}
	default:
		return key, false
	}
	identity := make(map[string]string, len(labels))
	for name, value := range labels {
		if name != receiverMetric && name != "receiver" && name != "integration" && name != "__name__" {
			identity[name] = value
		}
	}
	var valid bool
	key.target, valid = inputSeriesKey(identity, false)
	return key, valid
}

func (read notificationReceiverRead) signal(metric, target string, pair notificationReceiverPair, now time.Time) api.ObsNotificationSignal {
	key := notificationReceiverKey{metric, target, pair}
	if pair.receiver != "" || metric == receiverUp || metric == receiverRun {
		for _, index := range []map[notificationReceiverKey]float64{read.values, read.times} {
			count := 0
			for candidate := range index {
				if candidate.metric == metric && candidate.pair == pair {
					count++
				}
			}
			if count > 1 {
				return notificationReceiverUnavailable("invalid_series", 0)
			}
		}
	}
	var values, times []promql.VectorSample
	if value, ok := read.values[key]; ok {
		values = []promql.VectorSample{{Value: value}}
	}
	if value, ok := read.times[key]; ok {
		times = []promql.VectorSample{{Value: value}}
	}
	return notificationSignal(values, nil, times, nil, 0, now)
}

func notificationReceiverUnavailable(reason string, window time.Duration) api.ObsNotificationSignal {
	return api.ObsNotificationSignal{State: api.ObsHealthSignalUnavailable, Reason: reason, WindowSeconds: int(window.Seconds()), MaxAgeSeconds: int(api.ObsHealthFreshnessMaxAge.Seconds())}
}

func notificationReceiverOverall(out api.ObsNotificationReceiverHealth) (string, string) {
	state := "healthy"
	if out.Collector.State != api.ObsHealthSignalHealthy || out.Collector.Value == nil || *out.Collector.Value != 1 || out.Inventory.State != api.ObsHealthSignalHealthy {
		state = "partial"
	}
	for _, group := range out.Groups {
		if group.State == "degraded" {
			return "degraded", "receiver_fault"
		}
		if group.State != "healthy" {
			state = "partial"
		}
	}
	if state == "partial" {
		return state, "incomplete_evidence"
	}
	return state, ""
}
