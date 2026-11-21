package operatorobs

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimeCommandCronPrerequisite(c RuntimeHandoffCollection, after, now time.Time) RuntimeHandoffSignal {
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
	if c.Values.LastSuccessAt.Before(now.Add(-api.ObsCommandCronFreshnessMaxAge)) {
		return RuntimeHandoffSignal{State: "stale", Reason: "collection_stale"}
	}
	if c.Values.LastSuccessAt.Before(after.Add(-api.ObsHealthClockSkewTolerance)) {
		return RuntimeHandoffSignal{State: "unavailable", Reason: "collection_before_boundary"}
	}
	return RuntimeHandoffSignal{}
}

func runtimeCommandCronClassValuesValid(v RuntimeCommandCronClassValues, now time.Time) bool {
	if v.Schedules < 0 || v.Schedules > api.ObsRuntimeCommandCronSchedulesMax || v.UnknownAge < 0 || v.UnknownAge > v.Schedules {
		return false
	}
	if v.Schedules == v.UnknownAge {
		return v.OldestDueAt == nil && v.OldestKnownDueAgeSeconds == nil
	}
	return v.OldestDueAt != nil && v.OldestKnownDueAgeSeconds != nil && runtimeHandoffClockValid(v.OldestDueAt, now) && runtimeUptimeValid(*v.OldestKnownDueAgeSeconds) && math.Abs(*v.OldestKnownDueAgeSeconds-max(now.Sub(*v.OldestDueAt).Seconds(), 0)) <= time.Microsecond.Seconds()
}

func runtimeCommandCronClass(name string, status RuntimeBuilderSignal, raw map[string]float64, now time.Time) RuntimeCommandCronClass {
	out := RuntimeCommandCronClass{Class: name, RuntimeHandoffSignal: RuntimeHandoffSignal(status)}
	if status.State != "available" {
		return out
	}
	bad := func() RuntimeCommandCronClass {
		return RuntimeCommandCronClass{Class: name, RuntimeHandoffSignal: RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}}
	}
	for _, name := range []string{"schedules", "age_unknown"} {
		n := raw[name]
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > float64(api.ObsRuntimeCommandCronSchedulesMax) || math.Trunc(n) != n {
			return bad()
		}
	}
	oldest, ok := runtimeHandoffOrigin(raw["oldest_due_timestamp_seconds"], now)
	if !ok || !runtimeHandoffSampleClockValid(oldest, status.ObservedAt, now) {
		return bad()
	}
	v := RuntimeCommandCronClassValues{Schedules: int64(raw["schedules"]), UnknownAge: int64(raw["age_unknown"]), OldestDueAt: oldest}
	if oldest != nil {
		age := max(now.Sub(*oldest).Seconds(), 0)
		v.OldestKnownDueAgeSeconds = &age
	}
	if !runtimeCommandCronClassValuesValid(v, now) || runtimeCommandCronUnknownAgeClass(name) && v.Schedules != v.UnknownAge {
		return bad()
	}
	out.State, out.Values = "empty", &v
	if v.Schedules > 0 {
		out.State = "nonempty"
	}
	return out
}

func runtimeCommandCronSweepValuesValid(v RuntimeCommandCronSweepValues, now time.Time, observed *time.Time) bool {
	if !runtimeHandoffSampleClockValid(v.LastStartedAt, observed, now) || !runtimeHandoffSampleClockValid(v.LastSuccessAt, observed, now) {
		return false
	}
	if v.LastStartedAt == nil {
		return !v.Running && v.LastSuccessAt == nil
	}
	return !v.Running || v.LastSuccessAt == nil || !v.LastSuccessAt.After(*v.LastStartedAt)
}
func runtimeCommandCronSweep(s RuntimeBuilderSignal, raw map[string]float64, now time.Time) RuntimeCommandCronSweep {
	out := RuntimeCommandCronSweep{RuntimeHandoffSignal: RuntimeHandoffSignal(s)}
	if s.State != "available" {
		return out
	}
	bad := RuntimeCommandCronSweep{RuntimeHandoffSignal: RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}}
	if raw["sweep_running"] != 0 && raw["sweep_running"] != 1 {
		return bad
	}
	started, startedOK := runtimeHandoffOrigin(raw["sweep_last_start_timestamp_seconds"], now)
	success, successOK := runtimeHandoffOrigin(raw["sweep_last_success_timestamp_seconds"], now)
	v := RuntimeCommandCronSweepValues{Running: raw["sweep_running"] == 1, LastStartedAt: started, LastSuccessAt: success}
	if !startedOK || !successOK || !runtimeCommandCronSweepValuesValid(v, now, s.ObservedAt) {
		return bad
	}
	out.Values = &v
	return out
}
