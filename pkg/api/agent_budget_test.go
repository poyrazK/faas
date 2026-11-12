package api_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAgentBudgetDefaults(t *testing.T) {
	for plan, want := range map[api.Plan]int64{api.PlanFree: 0, api.PlanHobby: 500, api.PlanPro: 2500, api.PlanScale: 7500} {
		if got := plan.AgentBudgetDefaultCents(); got != want {
			t.Errorf("%s default = %d cents, want %d", plan, got, want)
		}
	}
}

func TestAgentStandingMillicents(t *testing.T) {
	// One 1016 MB instance is exactly 1 GB billed: 730 GB-h at $0.05.
	if got, want := api.AgentStandingMillicents(1, 1016), int64(730*api.OverageMillicentsPerGBHour); got != want {
		t.Fatalf("one GB-month = %d millicents, want %d", got, want)
	}
	if got := api.AgentStandingMillicents(3, 1016); got != 3*730*api.OverageMillicentsPerGBHour {
		t.Fatalf("three GB-months = %d", got)
	}
	if api.AgentStandingMillicents(0, 512) != 0 || api.AgentStandingMillicents(2, 0) != 0 {
		t.Fatal("no floor or no RAM must cost nothing")
	}
	// A prewarm of one GB for an hour is one GB-hour.
	if got := api.AgentPrewarmMillicents(1, 1016, 3600); got != api.OverageMillicentsPerGBHour {
		t.Fatalf("one GB-hour prewarm = %d", got)
	}
}

func TestEffectiveAgentBudget(t *testing.T) {
	ptr := func(v int64) *int64 { return &v }
	cases := []struct {
		name         string
		set, unlim   bool
		cents        int64
		orgMax       *int64
		want         int64
		wantSource   string
		wantLimitted bool
	}{
		{"plan default", false, false, 0, nil, 2500, api.AgentBudgetSourcePlanDefault, true},
		{"key budget", true, false, 9000, nil, 9000, api.AgentBudgetSourceKey, true},
		{"unlimited", true, true, 0, nil, 0, api.AgentBudgetSourceUnlimited, false},
		{"org ceiling caps key", true, false, 9000, ptr(4000), 4000, api.AgentBudgetSourceOrgCeiling, true},
		{"org ceiling caps unlimited", true, true, 0, ptr(4000), 4000, api.AgentBudgetSourceOrgCeiling, true},
		{"org ceiling above key", true, false, 1000, ptr(4000), 1000, api.AgentBudgetSourceKey, true},
		{"org ceiling caps default", false, false, 0, ptr(100), 100, api.AgentBudgetSourceOrgCeiling, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, source, limited := api.EffectiveAgentBudget(api.PlanPro, tc.set, tc.unlim, tc.cents, tc.orgMax)
			if limited != tc.wantLimitted || source != tc.wantSource || (limited && got != tc.want) {
				t.Fatalf("= %d %s %v; want %d %s %v", got, source, limited, tc.want, tc.wantSource, tc.wantLimitted)
			}
		})
	}
}
