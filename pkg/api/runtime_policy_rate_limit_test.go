package api

import "testing"

func TestEffectiveAppRequestRateLimits(t *testing.T) {
	plan := MustLimitsFor(PlanPro)
	cases := []struct {
		name               string
		rpsOverride        int
		burstOverride      int
		wantRPS, wantBurst int
	}{
		{name: "plan defaults", wantRPS: plan.RateLimitRPS, wantBurst: plan.RateLimitBurst},
		{name: "independent overrides", rpsOverride: 25, burstOverride: 120, wantRPS: 25, wantBurst: 120},
		{name: "zero restores each default", rpsOverride: 0, burstOverride: 0, wantRPS: plan.RateLimitRPS, wantBurst: plan.RateLimitBurst},
		{name: "direct-store values are clamped", rpsOverride: plan.RateLimitRPS + 1, burstOverride: plan.RateLimitBurst + 1, wantRPS: plan.RateLimitRPS, wantBurst: plan.RateLimitBurst},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			rps, burst := EffectiveAppRequestRateLimits(PlanPro, test.rpsOverride, test.burstOverride)
			if rps != test.wantRPS || burst != test.wantBurst {
				t.Fatalf("effective limits = %d/%d, want %d/%d", rps, burst, test.wantRPS, test.wantBurst)
			}
		})
	}
	if rps, burst := EffectiveAppRequestRateLimits(Plan("unknown"), 1, 1); rps != 0 || burst != 0 {
		t.Fatalf("unknown plan effective limits = %d/%d, want 0/0", rps, burst)
	}
}
