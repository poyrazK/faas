package billing

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type breakdownKey struct {
	route, unit, model, card string
	price                    int64
	trial                    bool
}

type ratedUnits struct {
	units, price              int64
	charged, included, waived bool
}

// ConsumerQuoteBreakdown attributes the already-rated usage to route or
// meter. Within a request minute, weighted routes take precedence when the
// source counts overlap, as in WeightAPIConsumerUsage. Shared allowance and
// tier portions then follow that same deterministic route order.
func ConsumerQuoteBreakdown(quote APIConsumerUsageQuote, cards []state.APIConsumerRateCard, usage []state.APIConsumerUsageBucket,
	routes []state.APIConsumerRouteUsageBucket) (byRoute, byMeter []api.APIConsumerQuoteBreakdown, err error) {
	cardByID := make(map[string]state.APIConsumerRateCard, len(cards))
	for _, card := range cards {
		cardByID[card.ID] = card
	}
	requests := make(map[time.Time]int64, len(usage))
	for _, row := range usage {
		requests[row.WindowStart.UTC()] = row.BillableUnits
	}
	routesByMinute := map[time.Time][]state.APIConsumerRouteUsageBucket{}
	for _, row := range routes {
		minute := row.WindowStart.UTC()
		routesByMinute[minute] = append(routesByMinute[minute], row)
	}
	routeTotals, meterTotals := map[breakdownKey]api.APIConsumerQuoteBreakdown{}, map[breakdownKey]api.APIConsumerQuoteBreakdown{}
	for _, bucket := range quote.Buckets {
		if bucket.BillableUnits == 0 {
			continue
		}
		card := cardByID[bucket.RateCardID]
		segments := ratedSegments(bucket, card)
		key := breakdownKey{unit: StreamUnit(bucket.Unit), model: bucket.Model, card: bucket.RateCardID, trial: bucket.Trial}
		if key.unit != state.APIConsumerRateCardUnitRequest {
			for _, segment := range segments {
				addBreakdown(meterTotals, key, segment, segment.units)
			}
			continue
		}
		minute := bucket.WindowStart.UTC()
		reportedRoutes := routesByMinute[minute]
		groups, err := routeUnitGroups(card.RouteWeights, requests[minute], reportedRoutes)
		if err != nil {
			return nil, nil, err
		}
		for _, group := range groups {
			key.route = group.route
			for remaining := group.units; remaining > 0; {
				if len(segments) == 0 {
					return nil, nil, fmt.Errorf("billing: route units exceed rated request units")
				}
				units := min(remaining, segments[0].units)
				addBreakdown(routeTotals, key, segments[0], units)
				remaining -= units
				segments[0].units -= units
				if segments[0].units == 0 {
					segments = segments[1:]
				}
			}
		}
		if len(segments) != 0 {
			return nil, nil, fmt.Errorf("billing: rated request units exceed route units")
		}
	}
	return sortedBreakdown(routeTotals), sortedBreakdown(meterTotals), nil
}

type routeUnitGroup struct {
	route string
	units int64
}

func routeUnitGroups(weights map[string]int64, requests int64, routes []state.APIConsumerRouteUsageBucket) ([]routeUnitGroup, error) {
	groups := make([]routeUnitGroup, 0, len(routes)+1)
	for _, weighted := range []bool{true, false} {
		for _, row := range routes {
			weight := max(1, weights[row.Route])
			if (weight > 1) != weighted || requests == 0 {
				continue
			}
			count := min(row.BillableUnits, requests)
			requests -= count
			units, err := multiplyMillicents(count, weight)
			if err != nil {
				return nil, err
			}
			if units > 0 {
				groups = append(groups, routeUnitGroup{route: row.Route, units: units})
			}
		}
	}
	if requests > 0 {
		groups = append(groups, routeUnitGroup{units: requests})
	}
	return groups, nil
}

func ratedSegments(bucket APIConsumerUsageChargeBucket, card state.APIConsumerRateCard) []ratedUnits {
	if bucket.RateCardID == "" && bucket.WaivedUnits > 0 {
		segments := []ratedUnits{{units: bucket.WaivedUnits, waived: true}}
		if remaining := bucket.BillableUnits - bucket.WaivedUnits; remaining > 0 {
			segments = append(segments, ratedUnits{units: remaining})
		}
		return segments
	}
	if bucket.RateCardID == "" || bucket.Trial {
		return []ratedUnits{{units: bucket.BillableUnits}}
	}
	if len(bucket.TierUnits) > 0 {
		segments := make([]ratedUnits, 0, len(bucket.TierUnits))
		for i, units := range bucket.TierUnits {
			if units > 0 {
				price := card.Tiers[i].PriceMillicentsPerUnit
				segments = append(segments, ratedUnits{units: units, price: price, charged: price > 0})
			}
		}
		return segments
	}
	segments := make([]ratedUnits, 0, 2)
	price := bucket.PriceMillicentsPerUnit
	if included := bucket.BillableUnits - bucket.ChargedUnits; included > 0 {
		segments = append(segments, ratedUnits{units: included, price: price, included: true})
	}
	if bucket.ChargedUnits > 0 {
		segments = append(segments, ratedUnits{units: bucket.ChargedUnits, price: price, charged: true})
	}
	return segments
}

func addBreakdown(totals map[breakdownKey]api.APIConsumerQuoteBreakdown, key breakdownKey, segment ratedUnits, units int64) {
	key.price = segment.price
	line := totals[key]
	line.Route, line.Unit, line.Model, line.RateCardID = key.route, key.unit, key.model, key.card
	line.PriceMillicentsPerUnit, line.Trial = key.price, key.trial
	line.BillableUnits += units
	if segment.waived {
		line.WaivedUnits += units
	}
	if segment.included {
		line.IncludedUnits += units
	}
	if segment.charged {
		line.ChargedUnits += units
		line.AmountMillicents += units * segment.price
	}
	totals[key] = line
}

func sortedBreakdown(totals map[breakdownKey]api.APIConsumerQuoteBreakdown) []api.APIConsumerQuoteBreakdown {
	out := make([]api.APIConsumerQuoteBreakdown, 0, len(totals))
	for _, line := range totals {
		out = append(out, line)
	}
	slices.SortFunc(out, func(a, b api.APIConsumerQuoteBreakdown) int {
		return cmp.Or(cmp.Compare(a.Route, b.Route), cmp.Compare(a.Unit, b.Unit), cmp.Compare(a.Model, b.Model),
			cmp.Compare(a.RateCardID, b.RateCardID), cmp.Compare(a.PriceMillicentsPerUnit, b.PriceMillicentsPerUnit),
			cmp.Compare(boolOrder(a.Trial), boolOrder(b.Trial)))
	})
	return out
}

func boolOrder(b bool) int {
	if b {
		return 1
	}
	return 0
}
