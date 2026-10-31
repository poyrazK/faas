package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
)

// Production-us rc.294: after a plain `gregale park`, inspect headlined
// h14-img "Needs attention", though a parked request app is scale to zero
// working and its next request wakes it (Hunt 14 H14-43).
func TestInspectParkedSeverityFollowsWhetherARequestWakesTheApp(t *testing.T) {
	for _, tc := range []struct {
		name, reason, mode     string
		wantSeverity, wantHead string
		wantMessage            string
	}{
		{"request app", "", "", "info", "Parked", "next request wakes it"},
		{"admin park", "admin_park", "", "info", "Parked", "next request wakes it"},
		{"crash loop", "liveness_exhausted", "", "error", "Needs attention", "failed liveness checks"},
		{"worker", "", "worker", "error", "Needs attention", "requests do not wake a worker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := main.InspectSummary{
				App:     main.InspectAppSummary{Slug: "h14-img", Status: "evicted_cold", ParkedReason: tc.reason, ExecutionMode: tc.mode},
				Release: main.InspectReleaseSummary{Available: true, Status: "live", TrafficPercent: 100},
				Runtime: main.InspectRuntimeSummary{Health: &main.InspectHealthSummary{Status: apihostingreceipt.SmokeVerified}},
			}
			summary.Recommendations = main.InspectRecommendations(summary)
			if len(summary.Recommendations) == 0 || summary.Recommendations[0].Code != "app_parked" ||
				summary.Recommendations[0].Severity != tc.wantSeverity || !strings.Contains(summary.Recommendations[0].Message, tc.wantMessage) {
				t.Fatalf("recommendations = %+v; want app_parked %s with %q", summary.Recommendations, tc.wantSeverity, tc.wantMessage)
			}
			state, _, command := main.InspectAttention(summary)
			if state != tc.wantHead || !strings.Contains(command, "h14-img") {
				t.Fatalf("headline = %q (next %q), want %q", state, command, tc.wantHead)
			}
		})
	}
}
