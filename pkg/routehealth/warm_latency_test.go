package routehealth

// adr: 20261028110044

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func int64Pointer(value int64) *int64 { return &value }

// The canary guard's latency minimum applies to the warm requests its p95
// ranked, not to every request (amendment 3).
func TestWarmLatencyMinimumUsesRankedRequests(t *testing.T) {
	now := time.Now().UTC()
	anchor := now.Add(-time.Hour)
	for _, tc := range []struct {
		name             string
		candidate, other *int64
		status, reason   string
	}{
		{"every request ranked", nil, nil, "regressed", "p95_latency_increased"},
		{"enough warm requests", int64Pointer(100), int64Pointer(150), "regressed", "p95_latency_increased"},
		{"candidate mostly cold boots", int64Pointer(99), int64Pointer(150), "unknown", "insufficient_latency_requests"},
		{"stable mostly cold boots", int64Pointer(150), int64Pointer(0), "unknown", "insufficient_latency_requests"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := latencyReport(now, true, 0, 850, 120, 150)
			for i := range r.Routes[0].Windows {
				w := &r.Routes[0].Windows[i]
				w.Candidate.LatencyRequests, w.Stable.LatencyRequests = tc.candidate, tc.other
			}
			Evaluate(&r, &anchor, "")
			if r.Routes[0].LatencyStatus != tc.status || r.Routes[0].Windows[0].LatencyReason != tc.reason {
				t.Fatalf("latency %s (%s), want %s (%s)", r.Routes[0].LatencyStatus, r.Routes[0].Windows[0].LatencyReason, tc.status, tc.reason)
			}
		})
	}
}

func TestWarmLatencyCountCannotExceedRequests(t *testing.T) {
	if valid(api.RouteHealthCounts{Requests: 10, LatencyRequests: int64Pointer(11)}) || valid(api.RouteHealthCounts{Requests: 10, LatencyRequests: int64Pointer(-1)}) {
		t.Fatal("accepted an impossible warm request count")
	}
	if !valid(api.RouteHealthCounts{Requests: 10, LatencyRequests: int64Pointer(10)}) || LatencySamples(api.RouteHealthCounts{Requests: 10}) != 10 {
		t.Fatal("rejected a full warm count or miscounted unmarked evidence")
	}
}
