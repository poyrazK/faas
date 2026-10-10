package routehealth

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// PooledWindows splits the stage observed so far into two equal, consecutive,
// minute-aligned windows that end with the newest closed one-minute window
// (ADR-953). ok is false when the stage is too short to pool.
func PooledWindows(anchor *time.Time, now time.Time) ([]api.RouteHealthWindowEvidence, bool) {
	if anchor == nil {
		return nil, false
	}
	minute := Windows(now)
	end := minute[len(minute)-1].End
	start := anchor.UTC().Truncate(api.RouteHealthWindow)
	if start.Before(anchor.UTC()) {
		start = start.Add(api.RouteHealthWindow)
	}
	if end.Sub(start) > api.RouteHealthPooledMaxSpan {
		start = end.Add(-api.RouteHealthPooledMaxSpan)
	}
	half := (end.Sub(start) / 2).Truncate(api.RouteHealthWindow)
	if 2*half < api.RouteHealthPooledMinSpan {
		return nil, false
	}
	return []api.RouteHealthWindowEvidence{
		{Start: end.Add(-2 * half), End: end.Add(-half)},
		{Start: end.Add(-half), End: end},
	}, true
}

// NeedsPooledEvidence reports whether a finding's one-minute summary is
// unknown only because its windows lacked requests. A regressed window is
// never pooled away, and other unknown reasons (anchor, telemetry,
// entitlement) stay.
func NeedsPooledEvidence(f api.RouteHealthFinding) bool {
	if f.Status != "unknown" {
		return false
	}
	insufficient := false
	for _, w := range f.Windows {
		for _, signal := range [][2]string{{w.ErrorStatus, w.ErrorReason}, {w.LatencyStatus, w.LatencyReason}} {
			switch {
			case signal[0] == "regressed":
				return false
			case signal[0] == "unknown" && (signal[1] == "insufficient_requests" || signal[1] == "insufficient_latency_requests"):
				insufficient = true
			case signal[0] == "unknown":
				return false
			}
		}
	}
	return insufficient
}

// applyPooledEvidence evaluates pooled_windows, when present, and adopts their
// verdict only for a sparse one-minute summary that pooling resolves. The
// one-minute windows always stay in the finding. It runs inside Evaluate, so
// every caller that re-derives verdicts reaches the same result.
func applyPooledEvidence(f *api.RouteHealthFinding, anchor *time.Time, unavailable string) {
	f.EvidenceWindow = ""
	if len(f.PooledWindows) != api.RouteHealthWindows {
		f.PooledWindows = nil
		return
	}
	evaluateWindows(f.PooledWindows, *f, anchor, unavailable)
	if unavailable != "" || !NeedsPooledEvidence(*f) {
		return
	}
	pooled := api.RouteHealthFinding{CheckLatency: f.CheckLatency, MaxP95MS: f.MaxP95MS, Windows: f.PooledWindows}
	SummarizeFinding(&pooled)
	if pooled.Status != "healthy" && pooled.Status != "regressed" {
		return
	}
	f.Status, f.Reason = pooled.Status, pooled.Reason
	f.ErrorStatus, f.ErrorReason = pooled.ErrorStatus, pooled.ErrorReason
	f.LatencyStatus, f.LatencyReason = pooled.LatencyStatus, pooled.LatencyReason
	f.EvidenceWindow = "pooled"
}

// SummarizeFindingWithPooled re-derives a saved finding's verdict, including
// any pooled evidence, for clients that validate reports.
func SummarizeFindingWithPooled(f *api.RouteHealthFinding, anchor *time.Time) {
	SummarizeFinding(f)
	applyPooledEvidence(f, anchor, "")
	applySyntheticEvidence(f, anchor, "")
}
