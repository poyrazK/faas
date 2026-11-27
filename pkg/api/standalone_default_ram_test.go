// adr: 20261122180000 — a standalone app's default RAM is capped at 512 MB.

package api

import "testing"

func TestStandaloneAppDefaultRAMMB(t *testing.T) {
	for plan, want := range map[Plan]int{PlanFree: 256, PlanHobby: 512, PlanPro: 512, PlanScale: 512} {
		if got := StandaloneAppDefaultRAMMB(MustLimitsFor(plan)); got != want {
			t.Errorf("%s default RAM = %d, want %d", plan, got, want)
		}
	}
}
