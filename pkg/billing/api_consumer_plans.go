package billing

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// PlanCardTimeline resolves the price history one consumer actually had
// (ADR-940). Assignments split time into segments; each segment is priced
// by its plan's cards, or the app default cards (empty PlanID) before the
// first assignment and after a return to default. The result is an ordinary
// card list, so allowances, tiers, weights, and statements price plan
// changes from the minute they take effect, and monthly counting continues
// across them. A segment's first card starts at the segment boundary with
// the card ID it was created with, so statement lines stay traceable.
func PlanCardTimeline(cards []state.APIConsumerRateCard, assignments []state.APIConsumerPlanAssignment) []state.APIConsumerRateCard {
	if len(assignments) == 0 {
		return defaultPlanCards(cards)
	}
	bySource := map[string][]state.APIConsumerRateCard{}
	for _, card := range cards {
		bySource[card.PlanID] = append(bySource[card.PlanID], card)
	}
	for _, history := range bySource {
		sort.Slice(history, func(i, j int) bool { return history[i].EffectiveFrom.Before(history[j].EffectiveFrom) })
	}
	ordered := append([]state.APIConsumerPlanAssignment(nil), assignments...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].EffectiveFrom.Before(ordered[j].EffectiveFrom) })

	type segment struct {
		plan       string
		start, end time.Time // zero start is the beginning of time; zero end is open
	}
	segments := []segment{{plan: "", end: ordered[0].EffectiveFrom}}
	for i, a := range ordered {
		seg := segment{plan: a.PlanID, start: a.EffectiveFrom}
		if i+1 < len(ordered) {
			seg.end = ordered[i+1].EffectiveFrom
		}
		segments = append(segments, seg)
	}

	var out []state.APIConsumerRateCard
	for _, seg := range segments {
		var opening *state.APIConsumerRateCard
		for _, card := range bySource[seg.plan] {
			inSegment := (seg.start.IsZero() || card.EffectiveFrom.After(seg.start)) && (seg.end.IsZero() || card.EffectiveFrom.Before(seg.end))
			switch {
			case !seg.start.IsZero() && !card.EffectiveFrom.After(seg.start):
				c := card
				opening = &c // the source's card in force when the segment opens
			case inSegment:
				out = append(out, card)
			}
		}
		if opening != nil {
			opening.EffectiveFrom = seg.start
			out = append(out, *opening)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EffectiveFrom.Before(out[j].EffectiveFrom) })
	return out
}

func defaultPlanCards(cards []state.APIConsumerRateCard) []state.APIConsumerRateCard {
	out := make([]state.APIConsumerRateCard, 0, len(cards))
	for _, card := range cards {
		if card.PlanID == "" {
			out = append(out, card)
		}
	}
	return out
}

// PlanPricedAt reports whether the source (a plan ID, or "" for the app
// default) has a card in force at the given minute. Assignments require it
// so a plan switch never leaves minutes priced by the previous plan.
func PlanPricedAt(cards []state.APIConsumerRateCard, planID string, at time.Time) bool {
	for _, card := range cards {
		if card.PlanID == planID && !card.EffectiveFrom.After(at) {
			return true
		}
	}
	return false
}
