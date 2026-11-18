package operatorobs

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func runtimePostCapturePrerequisite(c RuntimeHandoffCollection, after, now time.Time) RuntimeHandoffSignal {
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
	if c.Values.LastSuccessAt.Before(now.Add(-api.ObsPostCaptureFreshnessMaxAge)) {
		return RuntimeHandoffSignal{State: "stale", Reason: "collection_stale"}
	}
	if c.Values.LastSuccessAt.Before(after.Add(-api.ObsHealthClockSkewTolerance)) {
		return RuntimeHandoffSignal{State: "unavailable", Reason: "collection_before_boundary"}
	}
	return RuntimeHandoffSignal{}
}

func runtimePostCaptureClassValuesValid(v RuntimePostCaptureClassValues, now time.Time) bool {
	if v.Pending < 0 || v.Pending > api.ObsRuntimePostCapturePendingMax || v.UnknownAge < 0 || v.UnknownAge > v.Pending {
		return false
	}
	if v.Pending == v.UnknownAge {
		return v.OldestBoundaryAt == nil && v.OldestKnownAgeSeconds == nil
	}
	return v.OldestBoundaryAt != nil && v.OldestKnownAgeSeconds != nil && runtimeHandoffClockValid(v.OldestBoundaryAt, now) && runtimeUptimeValid(*v.OldestKnownAgeSeconds) && math.Abs(*v.OldestKnownAgeSeconds-max(now.Sub(*v.OldestBoundaryAt).Seconds(), 0)) <= time.Microsecond.Seconds()
}

func runtimePostCaptureClass(name string, status RuntimeBuilderSignal, raw map[string]float64, now time.Time) RuntimePostCaptureClass {
	out := RuntimePostCaptureClass{Class: name, RuntimeHandoffSignal: RuntimeHandoffSignal(status)}
	if status.State != "available" {
		return out
	}
	bad := func() RuntimePostCaptureClass {
		return RuntimePostCaptureClass{Class: name, RuntimeHandoffSignal: RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}}
	}
	for _, name := range []string{"pending", "age_unknown"} {
		n := raw[name]
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > float64(api.ObsRuntimePostCapturePendingMax) || math.Trunc(n) != n {
			return bad()
		}
	}
	oldest, ok := runtimeHandoffOrigin(raw["oldest_boundary_timestamp_seconds"], now)
	if !ok || !runtimeHandoffSampleClockValid(oldest, status.ObservedAt, now) {
		return bad()
	}
	v := RuntimePostCaptureClassValues{Pending: int64(raw["pending"]), UnknownAge: int64(raw["age_unknown"]), OldestBoundaryAt: oldest}
	if oldest != nil {
		age := max(now.Sub(*oldest).Seconds(), 0)
		v.OldestKnownAgeSeconds = &age
	}
	if !runtimePostCaptureClassValuesValid(v, now) || name == "unknown" && v.Pending != v.UnknownAge {
		return bad()
	}
	out.State, out.Values = "empty", &v
	if v.Pending > 0 {
		out.State = "nonempty"
	}
	return out
}
