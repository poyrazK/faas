package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us rc.297: inspect led a healthy worker (h14-wk) with "Review
// recommended: The live deployment has no verified health receipt" and
// "Configure a health endpoint and redeploy". A worker or job serves no HTTP
// requests, so there is nothing to probe (Hunt 14 H14-105).
func TestInspectSkipsHTTPHealthForWorkersAndJobs(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{{api.ExecutionModeWorker, false}, {api.ExecutionModeJob, false}, {"", true}} {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			summary := main.InspectSummary{
				App:     main.InspectAppSummary{Slug: "h14-wk", Type: "app", Status: "active", ExecutionMode: tc.mode},
				Release: main.InspectReleaseSummary{Available: true, Status: "live", TrafficPercent: 100},
			}
			got := false
			for _, rec := range main.InspectRecommendations(summary) {
				if rec.Code == "health_unverified" || rec.Code == "health_endpoint_missing" {
					got = true
				}
			}
			if got != tc.want {
				t.Fatalf("mode %q: HTTP health recommendation present = %v, want %v", tc.mode, got, tc.want)
			}
		})
	}
}
