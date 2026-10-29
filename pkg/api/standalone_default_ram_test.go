// adr: 20261029234500 — a standalone app's default RAM is capped at 1024 MB.

package api

import "testing"

func TestStandaloneAppDefaultRAMMB(t *testing.T) {
	for plan, want := range map[Plan]int{PlanFree: 256, PlanHobby: 512, PlanPro: 1024, PlanScale: 1024} {
		if got := StandaloneAppDefaultRAMMB(MustLimitsFor(plan)); got != want {
			t.Errorf("%s default RAM = %d, want %d", plan, got, want)
		}
	}
}
