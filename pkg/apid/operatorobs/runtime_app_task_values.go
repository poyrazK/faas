package operatorobs

import (
	"math"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimeAppTaskPrerequisite(c RuntimeHandoffCollection, after, now time.Time) RuntimeHandoffSignal {
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
	if c.Values.LastSuccessAt.Before(now.Add(-api.ObsAppTaskFreshnessMaxAge)) {
		return RuntimeHandoffSignal{State: "stale", Reason: "collection_stale"}
	}
	if c.Values.LastSuccessAt.Before(after.Add(-api.ObsHealthClockSkewTolerance)) {
		return RuntimeHandoffSignal{State: "unavailable", Reason: "collection_before_boundary"}
	}
	return RuntimeHandoffSignal{}
}

func runtimeAppTaskClassValuesValid(v RuntimeAppTaskClassValues, now time.Time) bool {
	if v.Pending < 0 || v.Pending > api.ObsRuntimeAppTaskPendingMax || v.UnknownAge < 0 || v.UnknownAge > v.Pending {
		return false
	}
	if v.Pending == v.UnknownAge {
		return v.OldestBoundaryAt == nil && v.OldestKnownAgeSeconds == nil
	}
	return v.OldestBoundaryAt != nil && v.OldestKnownAgeSeconds != nil && runtimeHandoffClockValid(v.OldestBoundaryAt, now) && runtimeUptimeValid(*v.OldestKnownAgeSeconds) && math.Abs(*v.OldestKnownAgeSeconds-max(now.Sub(*v.OldestBoundaryAt).Seconds(), 0)) <= time.Microsecond.Seconds()
}

func runtimeAppTaskClass(name string, status RuntimeBuilderSignal, raw map[string]float64, now time.Time) RuntimeAppTaskClass {
	out := RuntimeAppTaskClass{Class: name, RuntimeHandoffSignal: RuntimeHandoffSignal(status)}
	if status.State != "available" {
		return out
	}
	bad := func() RuntimeAppTaskClass {
		return RuntimeAppTaskClass{Class: name, RuntimeHandoffSignal: RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}}
	}
	for _, name := range []string{"pending", "age_unknown"} {
		n := raw[name]
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > float64(api.ObsRuntimeAppTaskPendingMax) || math.Trunc(n) != n {
			return bad()
		}
	}
	oldest, ok := runtimeHandoffOrigin(raw["oldest_boundary_timestamp_seconds"], now)
	if !ok || !runtimeHandoffSampleClockValid(oldest, status.ObservedAt, now) {
		return bad()
	}
	v := RuntimeAppTaskClassValues{Pending: int64(raw["pending"]), UnknownAge: int64(raw["age_unknown"]), OldestBoundaryAt: oldest}
	if oldest != nil {
		age := max(now.Sub(*oldest).Seconds(), 0)
		v.OldestKnownAgeSeconds = &age
	}
	if !runtimeAppTaskClassValuesValid(v, now) || runtimeAppTaskUnknownAgeClass(strings.Split(name, "/")[1]) && v.Pending != v.UnknownAge {
		return bad()
	}
	out.State, out.Values = "empty", &v
	if v.Pending > 0 {
		out.State = "nonempty"
	}
	return out
}

func runtimeAppTaskDispatchState(v RuntimeAppTaskDispatchValues) string {
	if !v.Enabled {
		return "disabled"
	}
	if v.Running {
		return "running"
	}
	return "stopped"
}
func runtimeAppTaskDispatchValuesValid(v RuntimeAppTaskDispatchValues, now time.Time, observed *time.Time) bool {
	return runtimeBuildRecoveryCountValid(v.SlotsConfigured) && v.SlotsConfigured > 0 && runtimeBuildRecoveryCountValid(v.SlotsInUse) && v.SlotsInUse <= v.SlotsConfigured && (v.Enabled || !v.Running) && runtimeHandoffSampleClockValid(v.LastClaimSuccessAt, observed, now) && runtimeHandoffSampleClockValid(v.LastSweepSuccessAt, observed, now)
}
func runtimeAppTaskDispatch(s RuntimeBuilderSignal, raw map[string]float64, now time.Time) RuntimeAppTaskDispatch {
	out := RuntimeAppTaskDispatch{RuntimeHandoffSignal: RuntimeHandoffSignal(s)}
	if s.State != "available" {
		return out
	}
	bad := RuntimeAppTaskDispatch{RuntimeHandoffSignal: RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}}
	if (raw["dispatch_enabled"] != 0 && raw["dispatch_enabled"] != 1) || (raw["runner_running"] != 0 && raw["runner_running"] != 1) || !runtimeBuildRecoveryNumberValid(raw["slots_configured"]) || !runtimeBuildRecoveryNumberValid(raw["slots_in_use"]) {
		return bad
	}
	claim, claimOK := runtimeHandoffOrigin(raw["claim_last_success_timestamp_seconds"], now)
	sweep, sweepOK := runtimeHandoffOrigin(raw["sweep_last_success_timestamp_seconds"], now)
	v := RuntimeAppTaskDispatchValues{Enabled: raw["dispatch_enabled"] == 1, Running: raw["runner_running"] == 1, SlotsConfigured: int64(raw["slots_configured"]), SlotsInUse: int64(raw["slots_in_use"]), LastClaimSuccessAt: claim, LastSweepSuccessAt: sweep}
	if !claimOK || !sweepOK || !runtimeAppTaskDispatchValuesValid(v, now, s.ObservedAt) {
		return bad
	}
	out.State, out.Values = runtimeAppTaskDispatchState(v), &v
	return out
}
