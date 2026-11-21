package operatorobs

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimeHandoffOrigin(value float64, now time.Time) (*time.Time, bool) {
	if value == 0 {
		return nil, true
	}
	at, ok := inputSampleTime(value, now)
	if !ok || at.Before(now.Add(-api.ObsRuntimeProcessUptimeMax)) {
		return nil, false
	}
	return &at, true
}

func runtimeHandoffClockValid(at *time.Time, now time.Time) bool {
	return at == nil || !at.IsZero() && !at.Before(now.Add(-api.ObsRuntimeProcessUptimeMax)) && !at.After(now.Add(api.ObsHealthClockSkewTolerance))
}

func runtimeHandoffSampleClockValid(at, sample *time.Time, now time.Time) bool {
	return runtimeHandoffClockValid(at, now) && (at == nil || sample != nil && !at.After(sample.Add(2*api.ObsHealthClockSkewTolerance)))
}

func runtimeHandoffBacklogPrerequisite(c RuntimeHandoffCollection, after, now time.Time) RuntimeHandoffSignal {
	if c.State != "available" {
		return c.RuntimeHandoffSignal
	}
	if c.Values == nil || c.Values.Success && c.Values.LastSuccessAt == nil {
		return RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}
	}
	if !c.Values.Success {
		reason := "collection_failed"
		if c.Values.LastSuccessAt == nil {
			reason = "collection_not_observed"
		}
		return RuntimeHandoffSignal{State: "unavailable", Reason: reason}
	}
	if c.Values.LastSuccessAt.Before(now.Add(-api.ObsBuildImageHandoffFreshnessMaxAge)) {
		return RuntimeHandoffSignal{State: "stale", Reason: "collection_stale"}
	}
	if c.Values.LastSuccessAt.Before(after.Add(-api.ObsHealthClockSkewTolerance)) {
		return RuntimeHandoffSignal{State: "unavailable", Reason: "collection_before_boundary"}
	}
	return RuntimeHandoffSignal{}
}

func runtimeHandoffCollection(status RuntimeBuilderSignal, raw map[string]float64, now time.Time) RuntimeHandoffCollection {
	out := RuntimeHandoffCollection{RuntimeHandoffSignal: RuntimeHandoffSignal(status)}
	if status.State != "available" {
		return out
	}
	at, ok := runtimeHandoffOrigin(raw["collection_last_success_timestamp_seconds"], now)
	if !ok || !runtimeHandoffSampleClockValid(at, status.ObservedAt, now) || raw["collection_success"] != 0 && raw["collection_success"] != 1 || raw["collection_success"] == 1 && at == nil {
		return RuntimeHandoffCollection{RuntimeHandoffSignal: RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}}
	}
	out.Values = &RuntimeHandoffCollectionValues{Success: raw["collection_success"] == 1, LastSuccessAt: at}
	return out
}

func runtimeHandoffBacklogValid(v RuntimeHandoffBacklogValues, now time.Time) bool {
	if v.Pending < 0 || v.Pending > api.ObsRuntimeHandoffPendingMax || !runtimeUptimeValid(v.OldestWaitSeconds) {
		return false
	}
	if v.Pending == 0 {
		return v.OldestFinishedAt == nil && v.OldestWaitSeconds == 0
	}
	return v.OldestFinishedAt != nil && runtimeHandoffClockValid(v.OldestFinishedAt, now) && math.Abs(v.OldestWaitSeconds-max(now.Sub(*v.OldestFinishedAt).Seconds(), 0)) <= time.Microsecond.Seconds()
}

func runtimeHandoffBacklog(status RuntimeBuilderSignal, raw map[string]float64, now time.Time) RuntimeHandoffBacklog {
	out := RuntimeHandoffBacklog{RuntimeHandoffSignal: RuntimeHandoffSignal(status)}
	if status.State != "available" {
		return out
	}
	pending := raw["pending"]
	oldest, ok := runtimeHandoffOrigin(raw["oldest_finished_timestamp_seconds"], now)
	if !ok || !runtimeHandoffSampleClockValid(oldest, status.ObservedAt, now) || pending < 0 || pending > float64(api.ObsRuntimeHandoffPendingMax) || math.Trunc(pending) != pending {
		return RuntimeHandoffBacklog{RuntimeHandoffSignal: RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}}
	}
	v := RuntimeHandoffBacklogValues{Pending: int64(pending), OldestFinishedAt: oldest}
	if oldest != nil {
		v.OldestWaitSeconds = max(now.Sub(*oldest).Seconds(), 0)
	}
	if !runtimeHandoffBacklogValid(v, now) {
		return RuntimeHandoffBacklog{RuntimeHandoffSignal: RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}}
	}
	out.State = "empty"
	if v.Pending > 0 {
		out.State = "waiting"
	}
	out.Values = &v
	return out
}

func runtimeHandoffRecoveryState(v RuntimeHandoffRecoveryValues, now time.Time) (string, bool) {
	if !runtimeHandoffClockValid(v.LastAttemptAt, now) || !runtimeHandoffClockValid(v.LastCompletedAt, now) || v.LastCompletedAt != nil && v.LastAttemptAt == nil {
		return "", false
	}
	if v.Inflight && (v.LastAttemptAt == nil || v.LastCompletedAt != nil && v.LastAttemptAt.Before(*v.LastCompletedAt)) {
		return "", false
	}
	if !v.Enabled {
		return "stopping", true
	}
	if v.Inflight {
		return "working", true
	}
	if v.LastAttemptAt == nil {
		return "not_started", true
	}
	if v.LastCompletedAt == nil || v.LastCompletedAt.Before(*v.LastAttemptAt) {
		return "", false
	}
	return "waiting", true
}

func runtimeHandoffRecovery(status RuntimeBuilderSignal, raw map[string]float64, now time.Time) RuntimeHandoffRecovery {
	out := RuntimeHandoffRecovery{RuntimeHandoffSignal: RuntimeHandoffSignal(status)}
	if status.State != "available" {
		return out
	}
	bad := RuntimeHandoffRecovery{RuntimeHandoffSignal: RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}}
	if raw["recovery_enabled"] != 0 && raw["recovery_enabled"] != 1 || raw["recovery_inflight"] != 0 && raw["recovery_inflight"] != 1 {
		return bad
	}
	attempt, ok := runtimeHandoffOrigin(raw["recovery_last_attempt_timestamp_seconds"], now)
	completed, completedOK := runtimeHandoffOrigin(raw["recovery_last_completed_timestamp_seconds"], now)
	if !ok || !completedOK || !runtimeHandoffSampleClockValid(attempt, status.ObservedAt, now) || !runtimeHandoffSampleClockValid(completed, status.ObservedAt, now) {
		return bad
	}
	v := RuntimeHandoffRecoveryValues{Enabled: raw["recovery_enabled"] == 1, Inflight: raw["recovery_inflight"] == 1, LastAttemptAt: attempt, LastCompletedAt: completed}
	state, valid := runtimeHandoffRecoveryState(v, now)
	if !valid {
		return bad
	}
	out.State, out.Values = state, &v
	return out
}
