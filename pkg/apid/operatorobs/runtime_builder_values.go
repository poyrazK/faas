package operatorobs

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimeBuilderCount(value float64) bool {
	return value >= 0 && value <= api.ObsRuntimeBuildSamplesMax && math.Trunc(value) == value
}

func runtimeBuilderOrigin(value float64, now time.Time) (*time.Time, bool) {
	if value == 0 {
		return nil, true
	}
	at, ok := inputSampleTime(value, now)
	if !ok || at.After(now) || at.Before(now.Add(-api.ObsRuntimeProcessUptimeMax)) {
		return nil, false
	}
	return &at, true
}

func runtimeBuilderWorkerState(v RuntimeBuilderWorkerValues, now time.Time) (string, float64, float64, bool) {
	validClock := func(at time.Time) bool {
		return !at.IsZero() && !at.After(now) && !at.Before(now.Add(-api.ObsRuntimeProcessUptimeMax))
	}
	if !validClock(v.StartedAt) || !runtimeUptimeValid(v.IntervalSeconds) || v.IntervalSeconds == 0 || !runtimeUptimeValid(v.BusyAllowanceSeconds) || v.BusyAllowanceSeconds == 0 || !v.Enabled && v.Inflight {
		return "", 0, 0, false
	}
	for _, at := range []*time.Time{v.LastAttemptAt, v.LastCompletedAt} {
		if at != nil && (!validClock(*at) || at.Before(v.StartedAt)) {
			return "", 0, 0, false
		}
	}
	if v.LastCompletedAt != nil && v.LastAttemptAt == nil || v.Inflight && v.LastAttemptAt == nil {
		return "", 0, 0, false
	}
	if v.LastAttemptAt != nil && v.LastCompletedAt != nil && (v.Inflight && v.LastAttemptAt.Before(*v.LastCompletedAt) || !v.Inflight && v.LastCompletedAt.Before(*v.LastAttemptAt)) {
		return "", 0, 0, false
	}
	origin, state := v.StartedAt, "waiting"
	// Match existing recording rules: two poll intervals plus the existing
	// 60-second scrape/evaluation margin, and the configured busy allowance.
	allowance := 2*v.IntervalSeconds + 2*api.ObsHealthClockSkewTolerance.Seconds()
	if v.Inflight {
		origin, state = *v.LastAttemptAt, "working"
		allowance = v.BusyAllowanceSeconds + 2*v.IntervalSeconds + 2*api.ObsHealthClockSkewTolerance.Seconds()
	} else if v.LastCompletedAt != nil {
		origin = *v.LastCompletedAt
	}
	age := now.Sub(origin).Seconds()
	if !runtimeUptimeValid(age) || !runtimeUptimeValid(allowance) {
		return "", 0, 0, false
	}
	if !v.Enabled {
		state = "draining"
	} else if age > allowance {
		state = "overdue"
	}
	return state, age, allowance, true
}

func runtimeBuilderWorker(status RuntimeBuilderSignal, raw map[string]float64, now time.Time) RuntimeBuilderWorker {
	out := RuntimeBuilderWorker{RuntimeBuilderSignal: status}
	if status.State != "available" {
		return out
	}
	bad := RuntimeBuilderWorker{RuntimeBuilderSignal: RuntimeBuilderSignal{State: "unavailable", Reason: "invalid_value"}}
	if raw["enabled"] != 0 && raw["enabled"] != 1 || raw["inflight"] != 0 && raw["inflight"] != 1 {
		return bad
	}
	started, ok := runtimeBuilderOrigin(raw["started_timestamp_seconds"], now)
	attempt, attemptOK := runtimeBuilderOrigin(raw["last_attempt_timestamp_seconds"], now)
	completed, completedOK := runtimeBuilderOrigin(raw["last_completed_timestamp_seconds"], now)
	if !ok || started == nil || !attemptOK || !completedOK {
		return bad
	}
	v := RuntimeBuilderWorkerValues{Enabled: raw["enabled"] == 1, Inflight: raw["inflight"] == 1, StartedAt: *started, LastAttemptAt: attempt, LastCompletedAt: completed, IntervalSeconds: raw["interval_seconds"], BusyAllowanceSeconds: raw["busy_allowance_seconds"]}
	state, age, allowance, valid := runtimeBuilderWorkerState(v, now)
	if !valid {
		return bad
	}
	v.ProgressAgeSeconds, v.ProgressAllowanceSeconds = age, allowance
	out.State, out.Values = state, &v
	return out
}

func runtimeBuilderSlots(status RuntimeBuilderSignal, raw map[string]float64) RuntimeBuilderSlots {
	out := RuntimeBuilderSlots{RuntimeBuilderSignal: status}
	if status.State != "available" {
		return out
	}
	if !runtimeBuilderCount(raw["in_use"]) || !runtimeBuilderCount(raw["limit"]) || raw["limit"] == 0 || raw["in_use"] > raw["limit"] {
		return RuntimeBuilderSlots{RuntimeBuilderSignal: RuntimeBuilderSignal{State: "unavailable", Reason: "invalid_value"}}
	}
	out.Values = &RuntimeBuilderSlotValues{Used: int(raw["in_use"]), Limit: int(raw["limit"])}
	return out
}

func runtimeBuilderQueueValid(v RuntimeBuilderQueueValues, now time.Time) bool {
	for _, n := range []int{v.Waiting, v.Active, v.Capacity, v.Workers} {
		if !runtimeBuilderCount(float64(n)) {
			return false
		}
	}
	if v.Workers == 0 || v.Active > v.Workers || v.Waiting+v.Active > v.Capacity+v.Workers {
		return false
	}
	if v.Waiting == 0 {
		return v.OldestEnqueuedAt == nil && v.OldestWaitSeconds == 0
	}
	return v.OldestEnqueuedAt != nil && !v.OldestEnqueuedAt.IsZero() && !v.OldestEnqueuedAt.After(now) && runtimeUptimeValid(now.Sub(*v.OldestEnqueuedAt).Seconds()) && v.OldestWaitSeconds == now.Sub(*v.OldestEnqueuedAt).Seconds()
}

func runtimeBuilderQueue(status RuntimeBuilderSignal, queue string, raw map[string]float64, now time.Time) RuntimeBuilderQueue {
	out := RuntimeBuilderQueue{RuntimeBuilderSignal: status, Queue: queue}
	if status.State != "available" {
		return out
	}
	bad := RuntimeBuilderQueue{RuntimeBuilderSignal: RuntimeBuilderSignal{State: "unavailable", Reason: "invalid_value"}, Queue: queue}
	for _, name := range []string{"waiting", "active", "capacity", "workers"} {
		if !runtimeBuilderCount(raw[name]) {
			return bad
		}
	}
	if raw["enabled"] != 0 && raw["enabled"] != 1 {
		return bad
	}
	oldest, ok := runtimeBuilderOrigin(raw["oldest_enqueued_timestamp_seconds"], now)
	if !ok {
		return bad
	}
	v := RuntimeBuilderQueueValues{Enabled: raw["enabled"] == 1, Waiting: int(raw["waiting"]), Active: int(raw["active"]), Capacity: int(raw["capacity"]), Workers: int(raw["workers"]), OldestEnqueuedAt: oldest}
	if oldest != nil {
		v.OldestWaitSeconds = now.Sub(*oldest).Seconds()
	}
	if !runtimeBuilderQueueValid(v, now) {
		return bad
	}
	out.Values = &v
	return out
}
