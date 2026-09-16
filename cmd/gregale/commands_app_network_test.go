package main

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppNetworkProbeState(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	rtt := 18
	tests := []struct {
		name   string
		row    api.DataUpstreamResponse
		want   string
		detail string
	}{
		{name: "missing", want: "stale", detail: "no probe observation"},
		{name: "old", row: api.DataUpstreamResponse{LastProbedAt: "2026-09-17T11:00:00Z", LastRTTMs: &rtt}, want: "stale", detail: "last probe is older than 15m"},
		{name: "fresh failure", row: api.DataUpstreamResponse{LastProbedAt: "2026-09-17T11:59:00Z"}, want: "failed", detail: "last probe had no successful RTT"},
		{name: "fresh success", row: api.DataUpstreamResponse{LastProbedAt: "2026-09-17T11:59:00Z", LastRTTMs: &rtt}, want: "fresh", detail: "last RTT 18ms"},
		{name: "invalid timestamp", row: api.DataUpstreamResponse{LastProbedAt: "not-a-time", LastRTTMs: &rtt}, want: "stale", detail: "last probe is older than 15m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, detail := appNetworkProbeState(tt.row, now)
			if got != tt.want || detail != tt.detail {
				t.Fatalf("state = %q (%q), want %q (%q)", got, detail, tt.want, tt.detail)
			}
		})
	}
}

func TestBuildAppNetworkDoctorReport(t *testing.T) {
	rtt := 9
	snapshot := appNetworkSnapshot{
		App:              appNetworkApp{Slug: "demo", Status: "ready"},
		EgressAllowlist:  []string{"203.0.113.0/24"},
		StaticEgress:     appNetworkStaticEgress{Available: true, PlanAllowed: true},
		ServiceDiscovery: appNetworkServiceDiscovery{Enabled: true},
		PrivateNetwork:   appNetworkPrivateNetwork{Status: "not_configured"},
		Upstreams: appNetworkUpstreams{
			Available: true,
			Count:     1,
			Quota:     8,
			Items: []api.DataUpstreamResponse{{
				HostLast4:    "a1b2c3d4",
				Port:         5432,
				LastRTTMs:    &rtt,
				LastProbedAt: "2026-09-17T11:59:00Z",
			}},
		},
		ObservedAt: "2026-09-17T12:00:00Z",
	}

	report := buildAppNetworkDoctorReport(snapshot, time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC))
	if !report.Healthy {
		t.Fatal("report should remain healthy when checks are advisory")
	}
	if len(report.Checks) != 5 {
		t.Fatalf("check count = %d, want 5", len(report.Checks))
	}
	for _, check := range report.Checks {
		if check.Name == "egress-policy" && check.Status != "ok" {
			t.Fatalf("egress policy status = %q, want ok", check.Status)
		}
		if check.Name == "upstream-telemetry" && check.Status != "ok" {
			t.Fatalf("upstream telemetry status = %q, want ok", check.Status)
		}
	}
}
