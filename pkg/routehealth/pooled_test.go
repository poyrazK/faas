package routehealth

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 846
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

// adr: 846
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

// adr: 846
func TestApplyPooledUsesVerdictsAndKeepsUnknown(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 20, 45, 0, time.UTC)
	anchor := now.Add(-12 * time.Minute)
	windows, ok := PooledWindows(&anchor, now)
	if !ok {
		t.Fatal("expected pooled windows")
	}
	pooled := func(path string, candidate, candidateErrors, stable int64) api.RouteHealthFinding {
		f := api.RouteHealthFinding{Method: "POST", Path: path, Windows: append([]api.RouteHealthWindowEvidence(nil), windows...)}
		for i := range f.Windows {
			f.Windows[i].Candidate = api.RouteHealthCounts{Requests: candidate, ServerErrors: candidateErrors}
			f.Windows[i].Stable = api.RouteHealthCounts{Requests: stable}
		}
		return f
	}
	sparse := [4]string{"unknown", "insufficient_requests", "", ""}
	report := api.RouteHealthReport{Routes: []api.RouteHealthFinding{
		{Method: "POST", Path: "/checkout", Status: "healthy", Reason: "comparisons_healthy"},
		func() api.RouteHealthFinding { f := pooledFinding(sparse, sparse); f.Path = "/refund"; return f }(),
		func() api.RouteHealthFinding { f := pooledFinding(sparse, sparse); f.Path = "/export"; return f }(),
		func() api.RouteHealthFinding { f := pooledFinding(sparse, sparse); f.Path = "/rare"; return f }(),
	}, Status: "unknown"}
	evaluated := api.RouteHealthReport{Routes: []api.RouteHealthFinding{
		pooled("/refund", 40, 0, 200), // healthy over the stage
		pooled("/export", 40, 8, 200), // 20% candidate 5xx in both halves
		pooled("/rare", 6, 0, 200),    // still too sparse
	}}
	Evaluate(&evaluated, &anchor, "")
	ApplyPooled(&report, evaluated.Routes)

	byPath := map[string]api.RouteHealthFinding{}
	for _, f := range report.Routes {
		byPath[f.Path] = f
	}
	if f := byPath["/refund"]; f.Status != "healthy" || f.EvidenceWindow != "pooled" {
		t.Errorf("/refund = %s/%q, want healthy pooled", f.Status, f.EvidenceWindow)
	}
	if f := byPath["/export"]; f.Status != "regressed" || f.EvidenceWindow != "pooled" {
		t.Errorf("/export = %s/%q, want regressed pooled", f.Status, f.EvidenceWindow)
	}
	if f := byPath["/rare"]; f.Status != "unknown" || f.EvidenceWindow != "" || f.Windows[0].ErrorReason != "insufficient_requests" {
		t.Errorf("/rare = %+v, want the original sparse finding", f)
	}
	if f := byPath["/checkout"]; f.EvidenceWindow != "" {
		t.Errorf("/checkout gained pooled evidence: %+v", f)
	}
	if report.Status != "regressed" {
		t.Errorf("report status = %s, want regressed", report.Status)
	}
}
