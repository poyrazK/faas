package routehealth

// adr: 458

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomaticRecoveryRequiresConfirmedRouteErrors(t *testing.T) {
	for _, scenario := range []string{"errors", "latency_only", "unknown", "mixed", "hold", "report", "missing_stable", "other_route_unknown"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Date(2026, 10, 2, 20, 0, 45, 0, time.UTC)
			anchor := now.Add(-time.Hour)
			finding := api.RouteHealthFinding{Method: "POST", Path: "/checkout", MaxP95MS: 300, Windows: Windows(now)}
			latency := 500.0
			stableLatency := 100.0
			for i := range finding.Windows {
				finding.Windows[i].Candidate = api.RouteHealthCounts{Requests: 100, ServerErrors: 10, P95LatencyMS: &latency}
				finding.Windows[i].Stable = api.RouteHealthCounts{Requests: 100, P95LatencyMS: &stableLatency}
			}
			report := api.RouteHealthReport{Mode: "enforce", OnRegression: "abort", StableDeploymentID: "stable", Routes: []api.RouteHealthFinding{finding}}
			switch scenario {
			case "latency_only":
				for i := range report.Routes[0].Windows {
					report.Routes[0].Windows[i].Candidate.ServerErrors = 0
				}
			case "unknown":
				for i := range report.Routes[0].Windows {
					report.Routes[0].Windows[i].Candidate.Requests = 10
				}
			case "mixed":
				report.Routes[0].Windows[0].Candidate.ServerErrors = 0
			case "hold":
				report.OnRegression = "hold"
			case "report":
				report.Mode = "report"
			case "missing_stable":
				report.StableDeploymentID = ""
			case "other_route_unknown":
				report.Routes = append(report.Routes, api.RouteHealthFinding{Method: "GET", Path: "/other", Windows: Windows(now)})
			}
			Evaluate(&report, &anchor, "")
			want := scenario == "errors" || scenario == "other_route_unknown"
			if AbortEligible(report) != want {
				t.Fatalf("abort=%v want=%v; status=%s", AbortEligible(report), want, report.Status)
			}
		})
	}
	if RegressionAction("") != "hold" {
		t.Fatal("legacy callers opted into abort")
	}
}
