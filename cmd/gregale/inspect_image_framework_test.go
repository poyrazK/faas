package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
)

// Production-us rc.297: inspect headlined a healthy nginx image deploy
// (h14-img3, verified 200) "Review recommended" because no framework was
// detected, and advised "Set an explicit start command or Dockerfile". An
// image runs its own ENTRYPOINT/CMD and has no source to detect a framework
// from (Hunt 14 H14-97).
func TestInspectFrameworkUnknownSkipsImageDeploys(t *testing.T) {
	for _, tc := range []struct {
		kind string
		want bool
	}{{"image", false}, {"source", true}, {"", true}} {
		t.Run("kind="+tc.kind, func(t *testing.T) {
			var unavailable []string
			runtime := main.InspectRuntime(api.AppResponse{}, &api.DeploymentResponse{Kind: tc.kind}, &unavailable)
			runtime.Framework = "unknown"
			runtime.Health = &main.InspectHealthSummary{Status: apihostingreceipt.SmokeVerified}
			summary := main.InspectSummary{
				App:     main.InspectAppSummary{Slug: "h14-img3", Type: "app", Status: "active"},
				Release: main.InspectReleaseSummary{Available: true, Status: "live", TrafficPercent: 100},
				Runtime: runtime,
			}
			got := false
			for _, rec := range main.InspectRecommendations(summary) {
				if rec.Code == "framework_unknown" {
					got = true
				}
			}
			if got != tc.want {
				t.Fatalf("kind %q: framework_unknown present = %v, want %v", tc.kind, got, tc.want)
			}
		})
	}
}
