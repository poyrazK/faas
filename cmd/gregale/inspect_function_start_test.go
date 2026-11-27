package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// prod 2026-11-20: `app health` on a `gregale add function` scaffold led with
// "Review recommended: No npm start script…". A function runs its handler
// under the platform runner and has no start script by design. Prod hunt r36:
// every Python function led with "no FastAPI, Flask, or Django dependency".
// A source-profile warning that is still about the source (an invalid
// package.json) keeps showing.
func TestInspectSkipsServerProfileWarningsForFunctions(t *testing.T) {
	for _, tc := range []struct {
		appType, code, framework string
		want                     bool
	}{
		{"function", "missing_start_command", "node", false},
		{"app", "missing_start_command", "node", true},
		{"function", "python_framework_not_detected", "python", false},
		{"app", "python_framework_not_detected", "python", true},
		{"function", "package_json_invalid", "node", true},
	} {
		t.Run(tc.appType+"/"+tc.code, func(t *testing.T) {
			summary := main.InspectSummary{
				App:     main.InspectAppSummary{Slug: "fn", Type: tc.appType, Status: "active"},
				Release: main.InspectReleaseSummary{Available: true, Status: "live", TrafficPercent: 100},
				Runtime: main.InspectRuntimeSummary{Framework: tc.framework, Warnings: []main.InspectRuntimeWarning{{Code: tc.code, Message: "warning"}}},
			}
			got := false
			for _, rec := range main.InspectRecommendations(summary) {
				if rec.Code == "profile_"+tc.code {
					got = true
				}
			}
			if got != tc.want {
				t.Fatalf("type %q: %s warning present = %v, want %v", tc.appType, tc.code, got, tc.want)
			}
		})
	}
}
