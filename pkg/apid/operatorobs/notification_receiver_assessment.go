package operatorobs

import (
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func (read notificationReceiverRead) metricKeys(metric string) []notificationReceiverKey {
	var out []notificationReceiverKey
	for key := range read.values {
		if key.metric == metric {
			out = append(out, key)
		}
	}
	return out
}

func receiverSignalUsable(signal api.ObsNotificationSignal) bool {
	return signal.State == api.ObsHealthSignalHealthy && signal.Value != nil
}

func receiverSignalIs(signal api.ObsNotificationSignal, value float64) bool {
	return receiverSignalUsable(signal) && *signal.Value == value
}

func receiverInvalid(signal api.ObsNotificationSignal, reason string) api.ObsNotificationSignal {
	signal.State, signal.Reason, signal.Value = api.ObsHealthSignalUnavailable, reason, nil
	return signal
}

func (read notificationReceiverRead) collector(now time.Time) (api.ObsNotificationSignal, string) {
	flags, clocks := read.metricKeys(receiverUp), read.metricKeys(receiverRun)
	if len(flags) == 0 {
		return notificationReceiverUnavailable("collector_missing", 0), ""
	}
	if len(flags) != 1 || len(clocks) > 1 {
		return notificationReceiverUnavailable("ambiguous_collector", 0), ""
	}
	target := flags[0].target
	flag := read.signal(receiverUp, target, notificationReceiverPair{}, now)
	if !receiverSignalUsable(flag) {
		return flag, target
	}
	if *flag.Value != 0 && *flag.Value != 1 {
		return receiverInvalid(flag, "invalid_collector_value"), target
	}
	for _, metric := range []string{receiverRun, "up"} {
		signal := read.signal(metric, target, notificationReceiverPair{}, now)
		if !receiverSignalUsable(signal) {
			return signal, target
		}
		if metric == "up" && *signal.Value != 1 {
			return receiverInvalid(signal, "node_scrape_unavailable"), target
		}
		if metric == receiverRun {
			at, valid := inputSampleTime(*signal.Value, now)
			if !valid {
				return receiverInvalid(signal, "invalid_collector_clock"), target
			}
			if at.Before(*flag.ObservedAt) {
				flag.ObservedAt = &at
			}
		}
		if signal.ObservedAt.Before(*flag.ObservedAt) {
			flag.ObservedAt = signal.ObservedAt
		}
	}
	if flag.ObservedAt.Before(now.Add(-api.ObsHealthFreshnessMaxAge)) {
		flag.State, flag.Reason, flag.Value = api.ObsHealthSignalStale, "observation_stale", nil
	}
	return flag, target
}

func (read notificationReceiverRead) inventory(now time.Time) (api.ObsNotificationSignal, string, map[notificationReceiverPair]bool) {
	keys := read.metricKeys(receiverExpected)
	expected := map[notificationReceiverPair]bool{}
	if len(keys) == 0 {
		return notificationReceiverUnavailable("inventory_missing", 0), "", expected
	}
	for key := range read.times {
		if key.metric == receiverExpected {
			if _, exists := read.values[key]; !exists {
				return notificationReceiverUnavailable("invalid_inventory", 0), "", expected
			}
		}
	}
	target := keys[0].target
	var oldest *time.Time
	for _, key := range keys {
		signal := read.signal(receiverExpected, target, key.pair, now)
		if key.target != target || !receiverSignalIs(signal, 1) {
			if key.target != target || receiverSignalUsable(signal) {
				signal = receiverInvalid(signal, "invalid_inventory")
			}
			return signal, target, expected
		}
		if oldest == nil || signal.ObservedAt.Before(*oldest) {
			oldest = signal.ObservedAt
		}
		expected[key.pair] = true
	}
	up := read.signal("up", target, notificationReceiverPair{}, now)
	if !receiverSignalIs(up, 1) {
		return receiverInvalid(up, "inventory_node_unavailable"), target, expected
	}
	if up.ObservedAt.Before(*oldest) {
		oldest = up.ObservedAt
	}
	count := float64(len(expected))
	return api.ObsNotificationSignal{State: api.ObsHealthSignalHealthy, Value: &count, ObservedAt: oldest, MaxAgeSeconds: int(api.ObsHealthFreshnessMaxAge.Seconds())}, target, expected
}

func (read notificationReceiverRead) assess(now time.Time) (api.ObsNotificationSignal, api.ObsNotificationSignal, []api.ObsNotificationReceiver) {
	collector, target := read.collector(now)
	inventory, intentTarget, expected := read.inventory(now)
	if target != "" && intentTarget != "" && target != intentTarget && receiverSignalUsable(inventory) {
		inventory = receiverInvalid(inventory, "inventory_source_mismatch")
	}
	pairs := map[notificationReceiverPair]bool{}
	// Range-query samples from a retired group do not prove it still exists.
	for _, index := range []map[notificationReceiverKey]float64{read.values, read.times} {
		for key := range index {
			if key.pair.receiver != "" {
				pairs[key.pair] = true
			}
		}
	}
	groups := []api.ObsNotificationReceiver{}
	if len(pairs) > api.ObsNotificationIntegrationsMax {
		return receiverInvalid(collector, "invalid_receiver_count"), receiverInvalid(inventory, "invalid_receiver_count"), groups
	}
	for pair := range pairs {
		group := api.ObsNotificationReceiver{Receiver: pair.receiver, Integration: pair.integration}
		if receiverSignalUsable(inventory) {
			value := expected[pair]
			group.Expected = &value
		}
		group.Requests = read.counter(receiverRequests, target, pair, collector, now)
		group.Failures = read.counter(receiverFailures, target, pair, collector, now)
		group.LastSuccessObservation = read.success(target, pair, collector, now)
		group.State, group.Reason = read.groupState(group, target, pair, collector, now)
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Receiver != groups[j].Receiver {
			return groups[i].Receiver < groups[j].Receiver
		}
		return groups[i].Integration < groups[j].Integration
	})
	return collector, inventory, groups
}

func (read notificationReceiverRead) counter(metric, target string, pair notificationReceiverPair, collector api.ObsNotificationSignal, now time.Time) api.ObsNotificationSignal {
	if !receiverSignalIs(collector, 1) {
		return notificationReceiverUnavailable("collector_unavailable", api.ObsNotificationWindow)
	}
	raw := read.signal(metric, target, pair, now)
	raw.WindowSeconds = int(api.ObsNotificationWindow.Seconds())
	if !receiverSignalUsable(raw) {
		return raw
	}
	if *raw.Value != math.Trunc(*raw.Value) {
		return receiverInvalid(raw, "invalid_counter")
	}
	requests := read.signal(receiverRequests, target, pair, now)
	failures := read.signal(receiverFailures, target, pair, now)
	if receiverSignalUsable(requests) && receiverSignalUsable(failures) && *failures.Value > *requests.Value {
		return receiverInvalid(raw, "inconsistent_counters")
	}
	index, err := read.requests, read.requestErr
	if metric == receiverFailures {
		index, err = read.failures, read.failureErr
	}
	var value []promql.VectorSample
	if increase, ok := index[notificationReceiverKey{metric, target, pair}]; ok {
		value = []promql.VectorSample{{Value: increase}}
	}
	return notificationSignal(value, err, []promql.VectorSample{{Value: float64(raw.ObservedAt.UnixMilli()) / 1000}}, nil, api.ObsNotificationWindow, now)
}

func (read notificationReceiverRead) success(target string, pair notificationReceiverPair, collector api.ObsNotificationSignal, now time.Time) api.ObsNotificationSignal {
	if !receiverSignalIs(collector, 1) {
		return notificationReceiverUnavailable("collector_unavailable", 0)
	}
	out := read.signal(receiverSuccess, target, pair, now)
	if receiverSignalIs(out, 0) {
		out.State, out.Reason, out.Value = api.ObsHealthSignalNoSamples, "success_not_observed", nil
	} else if receiverSignalUsable(out) {
		if _, valid := inputSampleTime(*out.Value, now); !valid {
			out = receiverInvalid(out, "invalid_success_clock")
		}
	}
	return out
}

func (read notificationReceiverRead) groupState(group api.ObsNotificationReceiver, target string, pair notificationReceiverPair, collector api.ObsNotificationSignal, now time.Time) (string, string) {
	if receiverSignalUsable(group.Failures) && *group.Failures.Value > 0 {
		return "degraded", "requests_failing"
	}
	if group.Expected == nil {
		return "partial", "inventory_unavailable"
	}
	if !receiverSignalIs(collector, 1) {
		return "partial", "collector_unavailable"
	}
	if !*group.Expected {
		for _, metric := range []string{receiverRequests, receiverFailures, receiverSuccess} {
			if receiverSignalUsable(read.signal(metric, target, pair, now)) {
				return "degraded", "unexpected_group"
			}
		}
		return "partial", "incomplete_evidence"
	}
	for _, metric := range []string{receiverRequests, receiverFailures, receiverSuccess} {
		if _, exists := read.values[notificationReceiverKey{metric, target, pair}]; !exists {
			return "degraded", "telemetry_missing"
		}
	}
	if !receiverSignalUsable(group.Requests) || !receiverSignalUsable(group.Failures) || !receiverSignalUsable(group.LastSuccessObservation) {
		return "partial", "incomplete_evidence"
	}
	return "healthy", ""
}
