package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Prod hunt r32: `release promote <id>` said only "unknown command"; the verb
// lives in one group, so the error names `release traffic promote`.
func TestReleaseNestedVerbNamesItsGroup(t *testing.T) {
	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"release", "promote", "08c0b864-bc2d-468d-979b-f23a7d4d15b8"})
	restore()
	if got := stderr(); code == 0 || !strings.Contains(got, "gregale release traffic promote") {
		t.Fatalf("exit %d stderr %q, want the traffic promote path", code, got)
	}
}

// Prod hunt r32: `release traffic promote <id> --app shop` called the id an
// app that differs from --app. A UUID is never a slug, so it fills
// --deployment; a vN stays a slug.
func TestTrafficPositionalDeploymentID(t *testing.T) {
	const id = "08c0b864-bc2d-468d-979b-f23a7d4d15b8"
	for _, tc := range []struct {
		name, slug, flag, wantSlug, wantDeployment string
		wantErr                                    bool
	}{
		{"uuid fills deployment", id, "", "", id, false},
		{"same uuid twice", id, id, "", id, false},
		{"different uuids", id, "5a470eed-f718-48a2-b3fa-1ad6a6b10c18", "", "", true},
		{"slug stays slug", "shop", "", "shop", "", false},
		{"revision stays slug", "v2", "", "v2", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deployment := tc.flag
			slug, err := main.LeadingDeploymentID(tc.slug, &deployment)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if slug != tc.wantSlug || deployment != tc.wantDeployment {
				t.Fatalf("slug %q deployment %q, want %q %q", slug, deployment, tc.wantSlug, tc.wantDeployment)
			}
		})
	}
}

// Prod hunt r32: every app on the default adaptive canary was "Review
// recommended" because no alert rule could roll the canary back, though the
// always-on 5xx, p95 and OOM checks do. The note stays, as information.
func TestCanaryWithoutAlertRuleIsNotAWarning(t *testing.T) {
	summary := main.InspectSummary{
		App:     main.InspectAppSummary{Slug: "demo"},
		Release: main.InspectReleaseSummary{Available: true, Status: "live", Scope: "production", TrafficPercent: 100, CanaryPreset: "adaptive", HealthSignalsAvailable: true},
	}
	found := false
	for _, rec := range main.InspectRecommendations(summary) {
		if rec.Code == "canary_without_health_gate" {
			found = true
			if rec.Severity != "info" {
				t.Fatalf("severity = %q, want info", rec.Severity)
			}
		}
	}
	if !found {
		t.Fatal("canary_without_health_gate note missing")
	}
	summary.Recommendations = main.InspectRecommendations(summary)
	if state, reason, _ := main.InspectAttention(summary); strings.Contains(reason, "canary") {
		t.Fatalf("attention = %q (%s) for a default canary with no alert rule", state, reason)
	}
}
