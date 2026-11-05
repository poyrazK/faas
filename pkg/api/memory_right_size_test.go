// adr: 20261102124306
package api

import "testing"

func TestRightSizeMemory(t *testing.T) {
	for _, tt := range []struct {
		name                     string
		plan                     Plan
		ramMB, totalMB, lowestMB int
		ok, fits                 bool
		suggested                int
		upgrade                  Plan
	}{
		{name: "fits", plan: PlanPro, ramMB: 512, totalMB: 490, lowestMB: 40, ok: true, fits: true, suggested: 704},
		{name: "next plan", plan: PlanFree, ramMB: 256, totalMB: 240, lowestMB: 20, ok: true, suggested: 384, upgrade: PlanHobby},
		{name: "nothing available", plan: PlanFree, ramMB: 256, totalMB: 240, lowestMB: 0, ok: true, suggested: 384, upgrade: PlanHobby},
		{name: "no plan is enough", plan: PlanScale, ramMB: 2048, totalMB: 2000, lowestMB: 100, ok: true, suggested: 2816},
		{name: "default ram", plan: PlanScale, totalMB: 1000, lowestMB: 100, ok: true, fits: true, suggested: 1344},
		{name: "always above current", plan: PlanPro, ramMB: 1024, totalMB: 1000, lowestMB: 700, ok: true, fits: false, suggested: 1088, upgrade: PlanScale},
		{name: "unknown plan", plan: "legacy", ramMB: 512, totalMB: 490, lowestMB: 40},
		{name: "no total", plan: PlanPro, ramMB: 512, lowestMB: 40},
		{name: "available above total", plan: PlanPro, ramMB: 512, totalMB: 490, lowestMB: 490},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := RightSizeMemory(tt.plan, tt.ramMB, tt.totalMB, tt.lowestMB)
			if ok != tt.ok || !ok {
				if ok != tt.ok {
					t.Fatalf("ok = %v, want %v", ok, tt.ok)
				}
				return
			}
			if got.SuggestedMB != tt.suggested || got.FitsPlan != tt.fits || got.UpgradePlan != tt.upgrade || got.SuggestedMB <= got.CurrentMB {
				t.Fatalf("got %+v", got)
			}
		})
	}
}
