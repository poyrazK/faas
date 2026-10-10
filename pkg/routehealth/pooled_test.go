package routehealth

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 944
func TestPooledWindowsSplitTheStageSoFar(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 20, 45, 0, time.UTC)
	end := Windows(now)[api.RouteHealthWindows-1].End
	if _, ok := PooledWindows(nil, now); ok {
		t.Fatal("pooled windows without an anchor")
	}
	short := end.Add(-3 * time.Minute)
	if _, ok := PooledWindows(&short, now); ok {
		t.Fatal("pooled a stage shorter than the minimum span")
	}
	anchor := end.Add(-10*time.Minute - 20*time.Second) // rounds up to a 10-minute span
	got, ok := PooledWindows(&anchor, now)
	if !ok || len(got) != api.RouteHealthWindows {
		t.Fatalf("PooledWindows = %+v, %t", got, ok)
	}
	if !got[1].End.Equal(end) || !got[0].End.Equal(got[1].Start) || got[0].End.Sub(got[0].Start) != 5*time.Minute || got[1].End.Sub(got[1].Start) != 5*time.Minute {
		t.Fatalf("pooled halves = %+v, want two consecutive 5-minute windows ending %s", got, end)
	}
	if got[0].Start.Before(anchor) {
		t.Fatalf("pooled window starts %s before the anchor %s", got[0].Start, anchor)
	}
	old := end.Add(-3 * time.Hour)
	capped, ok := PooledWindows(&old, now)
	if !ok || capped[1].End.Sub(capped[0].Start) != api.RouteHealthPooledMaxSpan {
		t.Fatalf("long stage pooled %+v, want the newest %s", capped, api.RouteHealthPooledMaxSpan)
	}
}

func pooledFinding(windows ...[4]string) api.RouteHealthFinding {
	f := api.RouteHealthFinding{Method: "POST", Path: "/refund", Status: "unknown", Reason: "comparisons_incomplete_or_unsettled"}
	for _, w := range windows {
		f.Windows = append(f.Windows, api.RouteHealthWindowEvidence{ErrorStatus: w[0], ErrorReason: w[1], LatencyStatus: w[2], LatencyReason: w[3]})
	}
	return f
}

// adr: 944
func TestNeedsPooledEvidenceOnlyForSparseRoutes(t *testing.T) {
	sparse := [4]string{"unknown", "insufficient_requests", "", ""}
	cases := []struct {
		name string
		f    api.RouteHealthFinding
		want bool
	}{
		{"both windows sparse", pooledFinding(sparse, sparse), true},
		{"sparse latency only", pooledFinding([4]string{"healthy", "comparison_healthy", "unknown", "insufficient_latency_requests"}, sparse), true},
		{"one window regressed", pooledFinding([4]string{"regressed", "server_error_rate_increased", "", ""}, sparse), false},
		{"anchor not elapsed", pooledFinding([4]string{"unknown", "observation_window_not_elapsed", "", ""}, sparse), false},
		{"already healthy", func() api.RouteHealthFinding { f := pooledFinding(sparse, sparse); f.Status = "healthy"; return f }(), false},
	}
	for _, tc := range cases {
		if got := NeedsPooledEvidence(tc.f); got != tc.want {
			t.Errorf("%s: NeedsPooledEvidence = %t, want %t", tc.name, got, tc.want)
		}
	}
}

// adr: 944
func TestEvaluateAdoptsPooledVerdictsAndKeepsMinuteWindows(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 20, 45, 0, time.UTC)
	anchor := now.Add(-12 * time.Minute)
	pooled, ok := PooledWindows(&anchor, now)
	if !ok {
		t.Fatal("expected pooled windows")
	}
	finding := func(path string, minuteCandidate, pooledCandidate, pooledErrors int64) api.RouteHealthFinding {
		f := api.RouteHealthFinding{Method: "POST", Path: path, Windows: Windows(now), PooledWindows: append([]api.RouteHealthWindowEvidence(nil), pooled...)}
		for i := range f.Windows {
			f.Windows[i].Candidate = api.RouteHealthCounts{Requests: minuteCandidate}
			f.Windows[i].Stable = api.RouteHealthCounts{Requests: 100}
		}
		for i := range f.PooledWindows {
			f.PooledWindows[i].Candidate = api.RouteHealthCounts{Requests: pooledCandidate, ServerErrors: pooledErrors}
			f.PooledWindows[i].Stable = api.RouteHealthCounts{Requests: 500}
		}
		return f
	}
	report := api.RouteHealthReport{Routes: []api.RouteHealthFinding{
		finding("/busy", 100, 500, 0), // one-minute evidence suffices; pooled ignored
		finding("/refund", 5, 40, 0),  // healthy over the stage
		finding("/export", 5, 40, 8),  // 20% candidate 5xx in both halves
		finding("/rare", 1, 6, 0),     // still too sparse
	}}
	Evaluate(&report, &anchor, "")

	byPath := map[string]api.RouteHealthFinding{}
	for _, f := range report.Routes {
		byPath[f.Path] = f
	}
	if f := byPath["/busy"]; f.Status != "healthy" || f.EvidenceWindow != "" {
		t.Errorf("/busy = %s/%q, want healthy from one-minute windows", f.Status, f.EvidenceWindow)
	}
	if f := byPath["/refund"]; f.Status != "healthy" || f.EvidenceWindow != "pooled" || f.Windows[0].ErrorReason != "insufficient_requests" {
		t.Errorf("/refund = %+v, want healthy pooled with sparse minute windows kept", f)
	}
	if f := byPath["/export"]; f.Status != "regressed" || f.EvidenceWindow != "pooled" {
		t.Errorf("/export = %s/%q, want regressed pooled", f.Status, f.EvidenceWindow)
	}
	if f := byPath["/rare"]; f.Status != "unknown" || f.EvidenceWindow != "" {
		t.Errorf("/rare = %s/%q, want unknown", f.Status, f.EvidenceWindow)
	}
	if report.Status != "regressed" {
		t.Errorf("report status = %s, want regressed", report.Status)
	}

	// Re-deriving a saved finding reaches the same verdict.
	saved := byPath["/export"]
	saved.Status, saved.EvidenceWindow = "", ""
	SummarizeFindingWithPooled(&saved, &anchor)
	if saved.Status != "regressed" || saved.EvidenceWindow != "pooled" {
		t.Errorf("re-derived /export = %s/%q", saved.Status, saved.EvidenceWindow)
	}

	// Unavailable telemetry never adopts pooled evidence.
	blocked := api.RouteHealthReport{Routes: []api.RouteHealthFinding{finding("/refund", 5, 40, 0)}}
	Evaluate(&blocked, &anchor, "telemetry_not_entitled")
	if blocked.Routes[0].EvidenceWindow != "" || blocked.Routes[0].Status != "unknown" {
		t.Errorf("unavailable report adopted pooled evidence: %+v", blocked.Routes[0])
	}
}
