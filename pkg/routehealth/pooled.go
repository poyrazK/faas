package routehealth

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// PooledWindows splits the stage observed so far into two equal, consecutive,
// minute-aligned windows that end with the newest closed one-minute window
// (ADR-846). ok is false when the stage is too short to pool.
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

// NeedsPooledEvidence reports whether a finding is unknown only because its
// one-minute windows lacked requests. A regressed window is never pooled
// away, and other unknown reasons (anchor, telemetry, entitlement) stay.
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

// ApplyPooled replaces a finding with its pooled evaluation when that
// evaluation reaches a verdict, then recomputes the report status. pooled must
// hold findings already evaluated over PooledWindows, in any order.
func ApplyPooled(report *api.RouteHealthReport, pooled []api.RouteHealthFinding) {
	for _, p := range pooled {
		if p.Status != "healthy" && p.Status != "regressed" {
			continue
		}
		for i := range report.Routes {
			if f := &report.Routes[i]; f.Method == p.Method && f.Path == p.Path && NeedsPooledEvidence(*f) {
				p.EvidenceWindow = "pooled"
				*f = p
			}
		}
	}
	if len(report.Routes) == 0 {
		return
	}
	report.Status, report.Reason = "healthy", "comparisons_healthy"
	for _, f := range report.Routes {
		report.Status, report.Reason = combine(report.Status, report.Reason, f.Status, f.Reason)
	}
}
