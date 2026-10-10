package routemonitor

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

// PooledWindows reuses route health's stage pooling (ADR-953). For a long
// serving deployment it is the newest RouteHealthPooledMaxSpan in two halves.
func PooledWindows(anchor *time.Time, now time.Time) ([]api.RouteMonitorWindow, bool) {
	windows, ok := routehealth.PooledWindows(anchor, now)
	if !ok {
		return nil, false
	}
	out := make([]api.RouteMonitorWindow, 0, len(windows))
	for _, w := range windows {
		out = append(out, api.RouteMonitorWindow{Start: w.Start, End: w.End})
	}
	return out, true
}

// NeedsPooledEvidence reports whether a finding's one-minute summary is
// unknown only because its windows were too sparse. A violated window is
// never pooled away.
func NeedsPooledEvidence(f api.RouteMonitorFinding) bool {
	if f.Status != "unknown" {
		return false
	}
	sparse := false
	for _, w := range f.Windows {
		for _, signal := range [][2]string{{w.ErrorStatus, w.ErrorReason}, {w.LatencyStatus, w.LatencyReason}} {
			switch {
			case signal[0] == "violated":
				return false
			case signal[0] == "unknown" && (signal[1] == "insufficient_requests" || signal[1] == "insufficient_errors" || signal[1] == "insufficient_latency_requests"):
				sparse = true
			case signal[0] == "unknown":
				return false
			}
		}
	}
	return sparse
}

// applyPooledEvidence adopts the pooled verdict only for a sparse one-minute
// summary that pooling resolves; one-minute windows always stay. It runs
// inside evaluateFinding so report validation re-derives the same result.
func applyPooledEvidence(f *api.RouteMonitorFinding, anchor *time.Time, unavailable string) {
	f.EvidenceWindow = ""
	if len(f.PooledWindows) != api.RouteHealthWindows {
		f.PooledWindows = nil
		return
	}
	pooled := api.RouteMonitorFinding{Route: f.Route, Windows: f.PooledWindows}
	evaluateWindows(&pooled, anchor, unavailable)
	if unavailable != "" || !NeedsPooledEvidence(*f) || pooled.Status != "healthy" && pooled.Status != "violated" {
		return
	}
	f.Status, f.Reason, f.ErrorStatus, f.LatencyStatus = pooled.Status, pooled.Reason, pooled.ErrorStatus, pooled.LatencyStatus
	f.EvidenceWindow = "pooled"
}
