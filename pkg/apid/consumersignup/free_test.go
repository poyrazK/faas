package consumersignup

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261124094745: a plan is free only when every card in force or
// scheduled charges nothing, and it has at least one.
func TestPlanChargesNothing(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	card := func(plan, unit string, from time.Time, edit func(*state.APIConsumerRateCard)) state.APIConsumerRateCard {
		c := state.APIConsumerRateCard{PlanID: plan, Unit: unit, EffectiveFrom: from}
		if edit != nil {
			edit(&c)
		}
		return c
	}
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	for name, tc := range map[string]struct {
		cards []state.APIConsumerRateCard
		want  bool
	}{
		"no cards":   {nil, false},
		"other plan": {[]state.APIConsumerRateCard{card("other", "request", past, nil)}, false},
		"zero card":  {[]state.APIConsumerRateCard{card("free", "request", past, nil)}, true},
		"unit price": {[]state.APIConsumerRateCard{card("free", "request", past, func(c *state.APIConsumerRateCard) { c.PriceMillicentsPerUnit = 1 })}, false},
		"base fee":   {[]state.APIConsumerRateCard{card("free", "request", past, func(c *state.APIConsumerRateCard) { c.BaseFeeMillicentsPerMonth = 1 })}, false},
		"minimum":    {[]state.APIConsumerRateCard{card("free", "request", past, func(c *state.APIConsumerRateCard) { c.MinimumMillicentsPerMonth = 1 })}, false},
		"priced tier": {[]state.APIConsumerRateCard{card("free", "request", past, func(c *state.APIConsumerRateCard) {
			c.Tiers = []state.APIConsumerRateCardTier{{PriceMillicentsPerUnit: 2}}
		})}, false},
		"model price": {[]state.APIConsumerRateCard{card("free", "token", past, func(c *state.APIConsumerRateCard) {
			c.ModelPrices = map[string]int64{"gpt-4o": 500}
		})}, false},
		"zero model price": {[]state.APIConsumerRateCard{card("free", "token", past, func(c *state.APIConsumerRateCard) {
			c.ModelPrices = map[string]int64{"gpt-4o": 0}
		})}, true},
		"future charges": {[]state.APIConsumerRateCard{card("free", "request", past, nil), card("free", "request", future, func(c *state.APIConsumerRateCard) { c.PriceMillicentsPerUnit = 1 })}, false},
		"old charge superseded": {[]state.APIConsumerRateCard{
			card("free", "request", past.Add(-time.Hour), func(c *state.APIConsumerRateCard) { c.PriceMillicentsPerUnit = 1 }),
			card("free", "request", past, nil)}, true},
		"one unit charges": {[]state.APIConsumerRateCard{card("free", "request", past, nil),
			card("free", "tool_call", past, func(c *state.APIConsumerRateCard) { c.PriceMillicentsPerUnit = 1 })}, false},
	} {
		if got := PlanChargesNothing(tc.cards, "free", now); got != tc.want {
			t.Errorf("%s = %v; want %v", name, got, tc.want)
		}
	}
}
