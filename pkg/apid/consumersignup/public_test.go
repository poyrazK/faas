package consumersignup

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261021143000
func TestChosenPlan(t *testing.T) {
	for _, tc := range []struct {
		name      string
		offered   []string
		requested string
		want      string
		ok        bool
	}{
		{"default plan only", nil, "", "", true},
		{"default plan refuses a plan ID", nil, "p1", "", false},
		{"one plan is picked without an ID", []string{"p1"}, "", "p1", true},
		{"several plans need an ID", []string{"p1", "p2"}, "", "", false},
		{"an offered plan", []string{"p1", "p2"}, "p2", "p2", true},
		{"a plan not offered", []string{"p1"}, "p9", "p9", false},
	} {
		got, ok := chosenPlan(state.APIConsumerSignupSettings{PlanIDs: tc.offered}, tc.requested)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("%s: chosenPlan = %q, %t; want %q, %t", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// adr: 20261021143000
func TestCurrentPrices(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cards := []state.APIConsumerRateCard{
		{PlanID: "p1", Unit: "request", Currency: "USD", PriceMillicentsPerUnit: 10, EffectiveFrom: now.Add(-48 * time.Hour)},
		{PlanID: "p1", Unit: "request", Currency: "USD", PriceMillicentsPerUnit: 20, EffectiveFrom: now.Add(-time.Hour)},
		{PlanID: "p1", Unit: "request", Currency: "USD", PriceMillicentsPerUnit: 30, EffectiveFrom: now.Add(time.Hour)},
		{PlanID: "p1", Unit: "compute_second", Currency: "USD", PriceMillicentsPerUnit: 5, EffectiveFrom: now.Add(2 * time.Hour)},
		{PlanID: "p1", Unit: "compute_second", Currency: "USD", PriceMillicentsPerUnit: 6, EffectiveFrom: now.Add(3 * time.Hour)},
		{PlanID: "", Unit: "request", Currency: "USD", PriceMillicentsPerUnit: 99, EffectiveFrom: now.Add(-time.Hour)},
	}
	got := currentPrices(cards, "p1", now)
	if len(got) != 2 || got[0].Unit != "compute_second" || got[0].PriceMillicentsPerUnit != 5 ||
		got[1].Unit != "request" || got[1].PriceMillicentsPerUnit != 20 {
		t.Fatalf("prices = %+v; want the card in force, or the first to come", got)
	}
}
