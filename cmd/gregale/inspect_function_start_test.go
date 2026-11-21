package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// prod 2026-11-20: `app health` on a `gregale add function` scaffold led with
// "Review recommended: No npm start script…". A function runs its handler
// under the platform runner and has no start script by design.
func TestInspectSkipsMissingStartCommandForFunctions(t *testing.T) {
	for _, tc := range []struct {
		appType string
		want    bool
	}{{"function", false}, {"app", true}} {
		t.Run(tc.appType, func(t *testing.T) {
			summary := main.InspectSummary{
				App:     main.InspectAppSummary{Slug: "fn", Type: tc.appType, Status: "active"},
				Release: main.InspectReleaseSummary{Available: true, Status: "live", TrafficPercent: 100},
				Runtime: main.InspectRuntimeSummary{Framework: "node", Warnings: []main.InspectRuntimeWarning{{Code: "missing_start_command", Message: "No npm start script"}}},
			}
			got := false
			for _, rec := range main.InspectRecommendations(summary) {
				if rec.Code == "profile_missing_start_command" {
					got = true
				}
			}
			if got != tc.want {
				t.Fatalf("type %q: start-command warning present = %v, want %v", tc.appType, got, tc.want)
			}
		})
	}
}
