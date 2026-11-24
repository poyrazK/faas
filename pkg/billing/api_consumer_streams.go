package billing

import (
	"slices"
	"sort"
	"strings"
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
		return modelMeterUsage(meter, cards, usage, in.Meters), nil
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

// modelMeterUsage keeps the logical meter's timeline and shared allowance
// while ordering reports within a minute by their model. A model-qualified
// report is interpreted only under a card that specifies its alias.
func modelMeterUsage(meter string, cards []state.APIConsumerRateCard, usage []state.APIConsumerUsageBucket, meters map[string][]state.APIConsumerMeterUsageBucket) []state.APIConsumerUsageBucket {
	out := meterUsage(usage, meters[meter])
	prefix := state.ModelMeterName(meter, "")
	type modelMinute struct {
		minute int64
		model  string
	}
	counts := map[modelMinute]int64{}
	for name, rows := range meters {
		model, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		for _, row := range rows {
			counts[modelMinute{row.WindowStart.Unix(), model}] = row.Units
		}
	}
	if len(counts) == 0 {
		return out
	}

	ordered := slices.Clone(cards)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].EffectiveFrom.Before(ordered[j].EffectiveFrom) })
	rated := make([]state.APIConsumerUsageBucket, 0, len(out))
	nextCard := 0
	var models []string
	for _, bucket := range out {
		for nextCard < len(ordered) && !ordered[nextCard].EffectiveFrom.After(bucket.WindowStart) {
			models = models[:0]
			for model := range ordered[nextCard].ModelPrices {
				models = append(models, model)
			}
			slices.Sort(models)
			nextCard++
		}
		start := len(rated)
		if bucket.BillableUnits > 0 {
			rated = append(rated, bucket)
		}
		for _, model := range models {
			if units := counts[modelMinute{bucket.WindowStart.Unix(), model}]; units > 0 {
				line := bucket
				line.Model, line.BillableUnits = model, units
				rated = append(rated, line)
			}
		}
		if start == len(rated) {
			rated = append(rated, bucket)
		}
	}
	return rated
}

// QuoteConsumerUsage prices a consumer's usage minutes with every unit its
// cards price. usage must start at from's month and be ascending by minute,
// as for QuoteAPIConsumerUsageFrom. Cards of one unit keep single-timeline
// pricing exactly; with several units each stream is quoted on its own and
// a minute no stream prices yet is one unpriced request bucket.
func QuoteConsumerUsage(cards []state.APIConsumerRateCard, usage []state.APIConsumerUsageBucket, in ConsumerUsageInputs,
	from time.Time, opts QuoteOptions) (APIConsumerUsageQuote, error) {
	quote, err := quoteConsumerUsageStreams(cards, usage, in, from, opts)
	if err != nil {
		return APIConsumerUsageQuote{}, err
	}
	missing := UnpricedConsumerModelUsage(cards, in.Meters, from)
	ApplyConsumerModelWaivers(missing, opts.ModelWaivers)
	for _, bucket := range missing {
		if err := quote.add(bucket); err != nil {
			return APIConsumerUsageQuote{}, err
		}
	}
	if len(missing) > 0 {
		quote.Priced = quote.UnpricedUnits == 0 && quote.Currency != ""
		orderConsumerUsageCharges(quote.Buckets)
	}
	if slices.ContainsFunc(cards, func(card state.APIConsumerRateCard) bool { return !card.EffectiveUntil.IsZero() }) {
		quote.planRateHistory = cards
	}
	return quote, nil
}

func quoteConsumerUsageStreams(cards []state.APIConsumerRateCard, usage []state.APIConsumerUsageBucket, in ConsumerUsageInputs,
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
		_, appMeter := state.AppMeterOfUnit(unit)
		for i := range quote.Buckets {
			if quote.Buckets[i].RateCardID != "" || appMeter {
				quote.Buckets[i].Unit = bucketUnit(unit)
			}
		}
		return quote, err
	}
	var buckets []APIConsumerUsageChargeBucket
	currency := ""
	pricedMinutes := make([]bool, len(usage))
	for _, unit := range units {
		stream := orderedAPIConsumerRateCards(streams[unit])
		var sub []state.APIConsumerUsageBucket
		active := -1
		for i, bucket := range usage {
			for active+1 < len(stream) && !stream[active+1].EffectiveFrom.After(bucket.WindowStart) {
				active++
			}
			if active >= 0 && !apiConsumerCardEnded(stream[active], bucket.WindowStart) {
				sub = append(sub, bucket)
				pricedMinutes[i] = true
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
	for i, bucket := range usage {
		if minute := bucket.WindowStart.UTC(); !pricedMinutes[i] && !minute.Before(from) {
			buckets = append(buckets, APIConsumerUsageChargeBucket{WindowStart: minute, BillableUnits: bucket.BillableUnits})
		}
	}
	orderConsumerUsageCharges(buckets)
	quote := APIConsumerUsageQuote{Currency: currency, Buckets: make([]APIConsumerUsageChargeBucket, 0, len(buckets))}
	for _, bucket := range buckets {
		if err := quote.add(bucket); err != nil {
			return APIConsumerUsageQuote{}, err
		}
	}
	quote.Priced = quote.UnpricedUnits == 0
	return quote, nil
}

func orderConsumerUsageCharges(buckets []APIConsumerUsageChargeBucket) {
	sort.SliceStable(buckets, func(i, j int) bool {
		a, b := buckets[i], buckets[j]
		if !a.WindowStart.Equal(b.WindowStart) {
			return a.WindowStart.Before(b.WindowStart)
		}
		if a.Unit != b.Unit {
			return a.Unit < b.Unit
		}
		return a.Model < b.Model
	})
}

// ConsumerModelRatesBefore reports whether a consumer's effective price history
// includes any model-rated meter before the end of a usage window.
func ConsumerModelRatesBefore(cards []state.APIConsumerRateCard, until time.Time) bool {
	return slices.ContainsFunc(cards, func(card state.APIConsumerRateCard) bool {
		return len(card.ModelPrices) > 0 && card.EffectiveFrom.Before(until)
	})
}

// UnpricedConsumerModelUsage finds reported models missing from a meter's rate
// card at the time of use. A card with model prices reserves the meter's alias
// family from its effective minute. A separately priced exact meter keeps its
// own stream. Missing model units stay out of rated usage and allowances.
func UnpricedConsumerModelUsage(cards []state.APIConsumerRateCard, meters map[string][]state.APIConsumerMeterUsageBucket,
	from time.Time) []APIConsumerUsageChargeBucket {
	if len(meters) == 0 {
		return nil
	}
	_, streams := UnitStreams(cards)
	for _, history := range streams {
		sort.Slice(history, func(i, j int) bool { return history[i].EffectiveFrom.Before(history[j].EffectiveFrom) })
	}
	var missing []APIConsumerUsageChargeBucket
	for unit, history := range streams {
		meter, ok := state.AppMeterOfUnit(unit)
		firstModelCard := slices.IndexFunc(history, func(card state.APIConsumerRateCard) bool { return len(card.ModelPrices) > 0 })
		if !ok || firstModelCard < 0 {
			continue
		}
		prefix := state.ModelMeterName(meter, "")
		for reported, rows := range meters {
			model, ok := strings.CutPrefix(reported, prefix)
			if !ok || !state.ValidAppMeter(model) || strings.Contains(model, "__") {
				continue
			}
			standalone := streams[state.AppUnitPrefix+reported]
			for _, row := range rows {
				minute := row.WindowStart.UTC()
				if row.Units == 0 || minute.Before(from) || minute.Before(history[firstModelCard].EffectiveFrom) {
					continue
				}
				next := sort.Search(len(history), func(i int) bool { return history[i].EffectiveFrom.After(minute) })
				if apiConsumerCardEnded(history[next-1], minute) {
					continue
				}
				exact := sort.Search(len(standalone), func(i int) bool { return standalone[i].EffectiveFrom.After(minute) })
				if exact > 0 && !apiConsumerCardEnded(standalone[exact-1], minute) {
					continue
				}
				if _, priced := history[next-1].ModelPrices[model]; !priced {
					missing = append(missing, APIConsumerUsageChargeBucket{
						WindowStart: minute, Unit: unit, Model: model, BillableUnits: row.Units,
					})
				}
			}
		}
	}
	orderConsumerUsageCharges(missing)
	return missing
}

// ApplyConsumerModelWaivers matches approved quantities to their original
// model minutes. A later report can increase the remaining unpriced units.
func ApplyConsumerModelWaivers(missing []APIConsumerUsageChargeBucket, approvals []state.APIConsumerUsageWaiver) {
	if len(missing) == 0 || len(approvals) == 0 {
		return
	}
	covered := make(map[coverageKey]int64)
	for _, approval := range approvals {
		for _, minute := range approval.Minutes {
			key := coverageKey{minute: minute.WindowStart.Unix(), unit: approval.Unit, model: approval.Model}
			covered[key] += minute.Units
		}
	}
	for i := range missing {
		bucket := &missing[i]
		key := coverageKey{minute: bucket.WindowStart.Unix(), unit: bucket.Unit, model: bucket.Model}
		bucket.WaivedUnits = min(bucket.BillableUnits, covered[key])
	}
}
