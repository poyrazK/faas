package operatorobs

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimeBuildRecoveryPrerequisite(c RuntimeHandoffCollection, after, now time.Time) RuntimeHandoffSignal {
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
	if c.Values.LastSuccessAt.Before(now.Add(-api.ObsBuildRecoveryFreshnessMaxAge)) {
		return RuntimeHandoffSignal{State: "stale", Reason: "collection_stale"}
	}
	if c.Values.LastSuccessAt.Before(after.Add(-api.ObsHealthClockSkewTolerance)) {
		return RuntimeHandoffSignal{State: "unavailable", Reason: "collection_before_boundary"}
	}
	return RuntimeHandoffSignal{}
}

func runtimeBuildRecoveryCountValid(n int64) bool {
	return n >= 0 && n <= api.ObsRuntimeBuildRecoveryCountMax
}
func runtimeBuildRecoveryNumberValid(n float64) bool {
	return !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= float64(api.ObsRuntimeBuildRecoveryCountMax) && math.Trunc(n) == n
}

func runtimeBuildRecoveryPairValid(n int64, oldest *time.Time, age *float64, now time.Time) bool {
	if !runtimeBuildRecoveryCountValid(n) {
		return false
	}
	if n == 0 {
		return oldest == nil && age == nil
	}
	return oldest != nil && age != nil && runtimeHandoffClockValid(oldest, now) && runtimeUptimeValid(*age) && math.Abs(*age-max(now.Sub(*oldest).Seconds(), 0)) <= time.Microsecond.Seconds()
}

func runtimeBuildRecoveryAge(at *time.Time, now time.Time) *float64 {
	if at == nil {
		return nil
	}
	v := max(now.Sub(*at).Seconds(), 0)
	return &v
}

func runtimeBuildRecoveryRunning(s RuntimeBuilderSignal, raw map[string]float64, now time.Time) RuntimeBuildRecoveryRunning {
	out := RuntimeBuildRecoveryRunning{RuntimeHandoffSignal: RuntimeHandoffSignal(s)}
	if s.State != "available" {
		return out
	}
	bad := RuntimeBuildRecoveryRunning{RuntimeHandoffSignal: RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}}
	n := raw["faas_build_running_count"]
	oldest, ok := runtimeHandoffOrigin(raw["faas_build_running_oldest_started_timestamp_seconds"], now)
	if !ok || !runtimeBuildRecoveryNumberValid(n) || !runtimeHandoffSampleClockValid(oldest, s.ObservedAt, now) {
		return bad
	}
	v := RuntimeBuildRecoveryRunningValues{Count: int64(n), OldestStartedAt: oldest, OldestAgeSeconds: runtimeBuildRecoveryAge(oldest, now)}
	if !runtimeBuildRecoveryPairValid(v.Count, v.OldestStartedAt, v.OldestAgeSeconds, now) {
		return bad
	}
	out.State, out.Values = "empty", &v
	if v.Count > 0 {
		out.State = "nonempty"
	}
	return out
}

func runtimeBuildRecoveryCleanup(s RuntimeBuilderSignal, raw map[string]float64, now time.Time) RuntimeBuildRecoveryCleanup {
	out := RuntimeBuildRecoveryCleanup{RuntimeHandoffSignal: RuntimeHandoffSignal(s)}
	if s.State != "available" {
		return out
	}
	bad := RuntimeBuildRecoveryCleanup{RuntimeHandoffSignal: RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}}
	n, retried := raw["faas_build_vm_cleanup_pending"], raw["faas_build_vm_cleanup_retry_pending"]
	oldest, ok := runtimeHandoffOrigin(raw["faas_build_vm_cleanup_oldest_created_timestamp_seconds"], now)
	if !ok || !runtimeBuildRecoveryNumberValid(n) || !runtimeBuildRecoveryNumberValid(retried) || retried > n || !runtimeHandoffSampleClockValid(oldest, s.ObservedAt, now) {
		return bad
	}
	v := RuntimeBuildRecoveryCleanupValues{Pending: int64(n), Retried: int64(retried), OldestCreatedAt: oldest, OldestAgeSeconds: runtimeBuildRecoveryAge(oldest, now)}
	if !runtimeBuildRecoveryPairValid(v.Pending, v.OldestCreatedAt, v.OldestAgeSeconds, now) {
		return bad
	}
	out.State, out.Values = "empty", &v
	if v.Pending > 0 {
		out.State = "nonempty"
	}
	return out
}
