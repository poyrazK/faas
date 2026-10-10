package routemonitor

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func pooledMonitorReport(t *testing.T, minuteRequests, pooledRequests, pooledErrors int64) api.RouteMonitorReport {
	t.Helper()
	now := time.Date(2026, 10, 9, 12, 40, 45, 0, time.UTC)
	anchor := now.Add(-2 * time.Hour)
	budget := int64(100) // 1%
	r := NewReport(api.RouteMonitorConfig{AppID: uuid.NewString(), Enabled: true, Revision: 1, Routes: []api.RouteMonitorRoute{{Method: "POST", Path: "/refund", Max5xxRateBPS: &budget}}}, now)
	r.DeploymentID, r.ObservationAnchor = uuid.NewString(), &anchor
	for i := range r.Routes[0].Windows {
		r.Routes[0].Windows[i].Observed = api.RouteHealthCounts{Requests: minuteRequests}
	}
	pooled, ok := PooledWindows(&anchor, now)
	if !ok {
		t.Fatal("expected pooled windows")
	}
	for i := range pooled {
		pooled[i].Observed = api.RouteHealthCounts{Requests: pooledRequests, ServerErrors: pooledErrors}
	}
	r.Routes[0].PooledWindows = pooled
	Evaluate(&r, "")
	return r
}

// adr: 944
func TestMonitorPooledEvidence(t *testing.T) {
	healthy := pooledMonitorReport(t, 4, 60, 0)
	f := healthy.Routes[0]
	if f.Status != "healthy" || f.EvidenceWindow != "pooled" || healthy.Status != "healthy" || f.Windows[0].ErrorReason != "insufficient_requests" {
		t.Fatalf("healthy pooled finding = %+v (report %s)", f, healthy.Status)
	}
	if span := f.PooledWindows[1].End.Sub(f.PooledWindows[0].Start); span != api.RouteHealthPooledMaxSpan {
		t.Fatalf("long-serving deployment pooled %s, want the newest %s", span, api.RouteHealthPooledMaxSpan)
	}
	if err := ValidateReport(healthy); err != nil {
		t.Fatalf("ValidateReport rejected a pooled report: %v", err)
	}

	violated := pooledMonitorReport(t, 4, 60, 6) // 10% against a 1% budget
	if violated.Routes[0].Status != "violated" || violated.Routes[0].ErrorStatus != "violated" || violated.Status != "violated" || violated.Routes[0].EvidenceWindow != "pooled" {
		t.Fatalf("violated pooled finding = %+v", violated.Routes[0])
	}

	busy := pooledMonitorReport(t, 100, 60, 6) // one-minute evidence suffices
	if busy.Routes[0].EvidenceWindow != "" || busy.Routes[0].Status != "healthy" {
		t.Fatalf("busy route used pooled evidence: %+v", busy.Routes[0])
	}

	rare := pooledMonitorReport(t, 1, 10, 0) // still too sparse
	if rare.Routes[0].EvidenceWindow != "" || rare.Routes[0].Status != "unknown" {
		t.Fatalf("sparse pooled evidence produced a verdict: %+v", rare.Routes[0])
	}

	tampered := pooledMonitorReport(t, 4, 60, 0)
	tampered.Routes[0].PooledWindows[0].Start = tampered.Routes[0].PooledWindows[0].Start.Add(-time.Minute)
	if ValidateReport(tampered) == nil {
		t.Fatal("ValidateReport accepted shifted pooled windows")
	}
	claimed := pooledMonitorReport(t, 4, 60, 6)
	claimed.Routes[0].Status, claimed.Status = "healthy", "healthy"
	if ValidateReport(claimed) == nil {
		t.Fatal("ValidateReport accepted a verdict that contradicts pooled evidence")
	}
}

// adr: 944
func TestMonitorNeedsPooledEvidence(t *testing.T) {
	window := func(errStatus, errReason string) api.RouteMonitorWindow {
		return api.RouteMonitorWindow{ErrorStatus: errStatus, ErrorReason: errReason, LatencyStatus: "disabled", LatencyReason: "budget_not_selected"}
	}
	for _, tc := range []struct {
		name string
		f    api.RouteMonitorFinding
		want bool
	}{
		{"sparse", api.RouteMonitorFinding{Status: "unknown", Windows: []api.RouteMonitorWindow{window("unknown", "insufficient_requests"), window("unknown", "insufficient_errors")}}, true},
		{"one violated", api.RouteMonitorFinding{Status: "unknown", Windows: []api.RouteMonitorWindow{window("violated", "server_error_budget_exceeded"), window("unknown", "insufficient_requests")}}, false},
		{"anchor", api.RouteMonitorFinding{Status: "unknown", Windows: []api.RouteMonitorWindow{window("unknown", "observation_window_not_elapsed"), window("unknown", "insufficient_requests")}}, false},
		{"healthy", api.RouteMonitorFinding{Status: "healthy", Windows: []api.RouteMonitorWindow{window("healthy", "error_budget_satisfied"), window("healthy", "error_budget_satisfied")}}, false},
	} {
		if got := NeedsPooledEvidence(tc.f); got != tc.want {
			t.Errorf("%s: NeedsPooledEvidence = %t, want %t", tc.name, got, tc.want)
		}
	}
}
