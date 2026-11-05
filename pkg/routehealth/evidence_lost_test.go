package routehealth_test

// adr: 20261028110044

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func TestEvidenceLost(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	anchor := now.Add(-time.Hour)
	for _, tc := range []struct {
		unavailable string
		want        bool
	}{
		{unavailable: routehealth.TelemetryIncomplete, want: true},
		{unavailable: "telemetry_unavailable"},
		{unavailable: "stable_deployment_ambiguous_or_missing"},
		{},
	} {
		report := api.RouteHealthReport{Mode: "enforce", Routes: []api.RouteHealthFinding{{Method: "GET", Path: "/a", Windows: routehealth.Windows(now)}}}
		routehealth.Evaluate(&report, &anchor, tc.unavailable)
		if got := routehealth.EvidenceLost(report); got != tc.want {
			t.Errorf("unavailable %q: lost %v, want %v", tc.unavailable, got, tc.want)
		}
	}
}
