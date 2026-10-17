package billing

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Unit streams (ADR-1317): an app's cards of different units are parallel
// price histories. Each stream prices its own unit from the minute its first
// card is in force, with its own included units, steps and volume, and a
// consumer's minute can carry one bucket per stream.

// StreamUnit is a card's or bucket's unit; empty means request.
func StreamUnit(unit string) string {
	if unit == "" {
		return state.APIConsumerRateCardUnitRequest
	}
	return unit
}

// bucketUnit is how a statement bucket records a stream: request buckets
// keep an empty unit, as they did before streams existed.
func bucketUnit(unit string) string {
	if unit == state.APIConsumerRateCardUnitRequest {
		return ""
	}
	return unit
}

// UnitStreams groups cards by unit: request first, then units by name.
func UnitStreams(cards []state.APIConsumerRateCard) ([]string, map[string][]state.APIConsumerRateCard) {
	streams := map[string][]state.APIConsumerRateCard{}
	for _, card := range cards {
		unit := StreamUnit(card.Unit)
		streams[unit] = append(streams[unit], card)
	}
	units := make([]string, 0, len(streams))
	for unit := range streams {
		units = append(units, unit)
	}
	sort.Slice(units, func(i, j int) bool {
		if (units[i] == state.APIConsumerRateCardUnitRequest) != (units[j] == state.APIConsumerRateCardUnitRequest) {
			return units[i] == state.APIConsumerRateCardUnitRequest
		}
		return units[i] < units[j]
	})
	return units, streams
}

// ConsumerUsageInputs are what pricing reads beyond usage minutes: route
// counts for request cards with route weights (ADR-952) and app-reported
// units by meter (ADR-1318).
type ConsumerUsageInputs struct {
	Routes []state.APIConsumerRouteUsageBucket
	Meters map[string][]state.APIConsumerMeterUsageBucket
}

// streamUsage converts usage minutes into a stream's units: weighted
// requests, rounded compute seconds or response megabytes (ADR-1308), or
// the app's reported units.
func streamUsage(unit string, cards []state.APIConsumerRateCard, usage []state.APIConsumerUsageBucket, in ConsumerUsageInputs) ([]state.APIConsumerUsageBucket, error) {
	if meter, ok := state.AppMeterOfUnit(unit); ok {
		return meterUsage(usage, in.Meters[meter]), nil
	}
	weighted := false
	for _, card := range cards {
		weighted = weighted || len(card.RouteWeights) > 0
	}
	if weighted {
		var err error
		if usage, err = WeightAPIConsumerUsage(cards, usage, in.Routes); err != nil {
			return nil, err
		}
	}
	return MeterAPIConsumerUsage(cards, usage), nil
}

// meterUsage replaces each minute's units with the app's reported units
// for one meter; a minute without a report counts zero.
func meterUsage(usage []state.APIConsumerUsageBucket, rows []state.APIConsumerMeterUsageBucket) []state.APIConsumerUsageBucket {
	byMinute := map[int64]int64{}
	for _, row := range rows {
		byMinute[row.WindowStart.UTC().Unix()] += row.Units
	}
	out := make([]state.APIConsumerUsageBucket, 0, len(usage))
	for _, bucket := range usage {
		bucket.BillableUnits = byMinute[bucket.WindowStart.UTC().Unix()]
		out = append(out, bucket)
	}
	return out
}

// QuoteConsumerUsage prices a consumer's usage minutes with every unit its
// cards price. usage must start at from's month and be ascending by minute,
// as for QuoteAPIConsumerUsageFrom. Cards of one unit keep single-timeline
// pricing exactly; with several units each stream is quoted on its own and
// a minute no stream prices yet is one unpriced request bucket.
func QuoteConsumerUsage(cards []state.APIConsumerRateCard, usage []state.APIConsumerUsageBucket, in ConsumerUsageInputs,
	from time.Time, opts QuoteOptions) (APIConsumerUsageQuote, error) {
	units, streams := UnitStreams(cards)
	if len(units) <= 1 {
		unit := state.APIConsumerRateCardUnitRequest
		if len(units) == 1 {
			unit = units[0]
		}
		metered, err := streamUsage(unit, cards, usage, in)
		if err != nil {
			return APIConsumerUsageQuote{}, err
		}
		quote, err := QuoteAPIConsumerUsageWith(cards, metered, from, opts)
		for i := range quote.Buckets {
			if quote.Buckets[i].RateCardID != "" {
				quote.Buckets[i].Unit = bucketUnit(unit)
			}
		}
		return quote, err
	}
	var buckets []APIConsumerUsageChargeBucket
	currency := ""
	firstPriced := time.Time{}
	for _, unit := range units {
		stream := streams[unit]
		start := stream[0].EffectiveFrom
		for _, card := range stream {
			if card.EffectiveFrom.Before(start) {
				start = card.EffectiveFrom
			}
		}
		if firstPriced.IsZero() || start.Before(firstPriced) {
			firstPriced = start
		}
		var sub []state.APIConsumerUsageBucket
		for _, bucket := range usage {
			if !bucket.WindowStart.Before(start) {
				sub = append(sub, bucket)
			}
		}
		metered, err := streamUsage(unit, stream, sub, in)
		if err != nil {
			return APIConsumerUsageQuote{}, err
		}
		quote, err := QuoteAPIConsumerUsageWith(stream, metered, from, opts)
		if err != nil {
			return APIConsumerUsageQuote{}, err
		}
		if currency != "" && quote.Currency != currency {
			return APIConsumerUsageQuote{}, ErrMixedAPIConsumerRateCardCurrency
		}
		currency = quote.Currency
		for _, bucket := range quote.Buckets {
			bucket.Unit = bucketUnit(unit)
			buckets = append(buckets, bucket)
		}
	}
	for _, bucket := range usage {
		if minute := bucket.WindowStart.UTC(); minute.Before(firstPriced) && !minute.Before(from) {
			buckets = append(buckets, APIConsumerUsageChargeBucket{WindowStart: minute, BillableUnits: bucket.BillableUnits})
		}
	}
	sort.SliceStable(buckets, func(i, j int) bool {
		if !buckets[i].WindowStart.Equal(buckets[j].WindowStart) {
			return buckets[i].WindowStart.Before(buckets[j].WindowStart)
		}
		return buckets[i].Unit < buckets[j].Unit
	})
	quote := APIConsumerUsageQuote{Currency: currency, Buckets: make([]APIConsumerUsageChargeBucket, 0, len(buckets))}
	for _, bucket := range buckets {
		if err := quote.add(bucket); err != nil {
			return APIConsumerUsageQuote{}, err
		}
	}
	quote.Priced = quote.UnpricedUnits == 0
	return quote, nil
}

// streamCardsAt returns, for each unit stream, the card in force at
// minute; streams that have not started are absent.
func streamCardsAt(cards []state.APIConsumerRateCard, minute time.Time) []state.APIConsumerRateCard {
	units, streams := UnitStreams(cards)
	var out []state.APIConsumerRateCard
	for _, unit := range units {
		var current *state.APIConsumerRateCard
		for i, card := range streams[unit] {
			if !card.EffectiveFrom.After(minute) && (current == nil || card.EffectiveFrom.After(current.EffectiveFrom)) {
				current = &streams[unit][i]
			}
		}
		if current != nil {
			out = append(out, *current)
		}
	}
	return out
}
