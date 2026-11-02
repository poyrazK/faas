package api_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Each serving release needs its own instance, and an app runs at most its
// effective max_concurrency plus the rollout overlap (invariant 1).
func TestTrafficSplitMaxReleases(t *testing.T) {
	for _, tc := range []struct {
		name   string
		appMax int
		plan   api.Plan
		want   int
	}{
		{"default app on Scale runs one instance plus the overlap", 1, api.PlanScale, 2},
		{"raised max_concurrency widens the split", 4, api.PlanPro, 5},
		{"app setting above the plan is capped by the plan", 50, api.PlanPro, api.MustLimitsFor(api.PlanPro).MaxConcurrency + api.RolloutConcurrencyGrant},
		{"unset app setting uses the plan", 0, api.PlanScale, api.MustLimitsFor(api.PlanScale).MaxConcurrency + api.RolloutConcurrencyGrant},
		{"unknown plan sets no limit", 3, api.Plan("unknown"), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := api.TrafficSplitMaxReleases(tc.appMax, tc.plan); got != tc.want {
				t.Fatalf("TrafficSplitMaxReleases(%d, %s) = %d, want %d", tc.appMax, tc.plan, got, tc.want)
			}
		})
	}
}
