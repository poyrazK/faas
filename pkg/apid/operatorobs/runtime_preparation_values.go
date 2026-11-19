package operatorobs

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimePreparationPrerequisite(c RuntimeHandoffCollection, after, now time.Time) RuntimeHandoffSignal {
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
	if c.Values.LastSuccessAt.Before(now.Add(-api.ObsImagePreparationFreshnessMaxAge)) {
		return RuntimeHandoffSignal{State: "stale", Reason: "collection_stale"}
	}
	if c.Values.LastSuccessAt.Before(after.Add(-api.ObsHealthClockSkewTolerance)) {
		return RuntimeHandoffSignal{State: "unavailable", Reason: "collection_before_boundary"}
	}
	return RuntimeHandoffSignal{}
}

func runtimePreparationPhaseValuesValid(v RuntimePreparationPhaseValues, now time.Time) bool {
	if v.Pending < 0 || v.Pending > api.ObsRuntimePreparationPendingMax || v.UnknownAge < 0 || v.UnknownAge > v.Pending {
		return false
	}
	if v.Pending == v.UnknownAge {
		return v.OldestPhaseStartedAt == nil && v.OldestKnownAgeSeconds == nil
	}
	return v.OldestPhaseStartedAt != nil && v.OldestKnownAgeSeconds != nil && runtimeHandoffClockValid(v.OldestPhaseStartedAt, now) && runtimeUptimeValid(*v.OldestKnownAgeSeconds) && math.Abs(*v.OldestKnownAgeSeconds-max(now.Sub(*v.OldestPhaseStartedAt).Seconds(), 0)) <= time.Microsecond.Seconds()
}

func runtimePreparationPhase(name string, status RuntimeBuilderSignal, raw map[string]float64, now time.Time) RuntimePreparationPhase {
	out := RuntimePreparationPhase{Phase: name, RuntimeHandoffSignal: RuntimeHandoffSignal(status)}
	if status.State != "available" {
		return out
	}
	bad := func() RuntimePreparationPhase {
		return RuntimePreparationPhase{Phase: name, RuntimeHandoffSignal: RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}}
	}
	for _, name := range []string{"pending", "phase_age_unknown"} {
		n := raw[name]
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > float64(api.ObsRuntimePreparationPendingMax) || math.Trunc(n) != n {
			return bad()
		}
	}
	oldest, ok := runtimeHandoffOrigin(raw["oldest_phase_started_timestamp_seconds"], now)
	if !ok || !runtimeHandoffSampleClockValid(oldest, status.ObservedAt, now) {
		return bad()
	}
	v := RuntimePreparationPhaseValues{Pending: int64(raw["pending"]), UnknownAge: int64(raw["phase_age_unknown"]), OldestPhaseStartedAt: oldest}
	if oldest != nil {
		age := max(now.Sub(*oldest).Seconds(), 0)
		v.OldestKnownAgeSeconds = &age
	}
	if !runtimePreparationPhaseValuesValid(v, now) {
		return bad()
	}
	out.State, out.Values = "empty", &v
	if v.Pending > 0 {
		out.State = "waiting"
	}
	return out
}
