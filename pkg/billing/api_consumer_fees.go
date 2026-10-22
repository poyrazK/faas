package billing

import (
	"math/big"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// FeeInstant is when a consumer's or tenant's month starts for fees
// (ADR-1316, ADR-1322): 00:00 UTC on the 1st, or its creation if later. ok
// is false when it did not exist in the month.
func FeeInstant(month, created time.Time) (time.Time, bool) {
	start := MonthStart(month)
	at := start
	if created := created.UTC().Truncate(time.Minute); created.After(at) {
		at = created
	}
	return at, at.Before(start.AddDate(0, 1, 0))
}

// ProrateMonthlyFee scales a month's fee to the share of the month left
// after at (ADR-1325), counted in whole minutes and rounded half up to the
// millicent. A fee charged from the month's first minute is unchanged.
func ProrateMonthlyFee(amount int64, at time.Time) int64 {
	start := MonthStart(at)
	return ProrateMonthlyFeeBetween(amount, at, start.AddDate(0, 1, 0))
}

// ProrateMonthlyFeeBetween scales a month's fee to the share of the month
// in [from, until) (ADR-20261019100332), in whole minutes rounded half up
// to the millicent. Both bounds fall in from's month.
func ProrateMonthlyFeeBetween(amount int64, from, until time.Time) int64 {
	start := MonthStart(from)
	end := start.AddDate(0, 1, 0)
	from, until = from.UTC().Truncate(time.Minute), until.UTC().Truncate(time.Minute)
	if until.After(end) {
		until = end
	}
	if amount <= 0 || !until.After(from) {
		return min(amount, 0)
	}
	total := int64(end.Sub(start) / time.Minute)
	share := int64(until.Sub(from) / time.Minute)
	if share >= total {
		return amount
	}
	scaled := new(big.Int).Mul(big.NewInt(amount), big.NewInt(share))
	scaled.Add(scaled, big.NewInt(total/2))
	scaled.Quo(scaled, big.NewInt(total))
	return scaled.Int64()
}

// MonthlyFeeCardEffectiveIn reports whether a card with a base fee or a
// minimum is in force for any minute of [start, end). Such periods must be
// whole UTC months, so a month's fees are billed by one statement.
func MonthlyFeeCardEffectiveIn(cards []state.APIConsumerRateCard, start, end time.Time) bool {
	_, streams := UnitStreams(cards)
	for _, stream := range streams {
		ordered := append([]state.APIConsumerRateCard(nil), stream...)
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].EffectiveFrom.Before(ordered[j].EffectiveFrom) })
		for i, card := range ordered {
			until := end
			if i+1 < len(ordered) {
				until = ordered[i+1].EffectiveFrom
			}
			if card.MonthlyFees() && card.EffectiveFrom.Before(end) && until.After(start) {
				return true
			}
		}
	}
	return false
}

// feeCard is a card's fee terms within its stream, reduced to what fees
// read. from and until bound the minutes a prorating card charges for in
// the month (ADR-20261019100332).
type feeCard struct {
	id, stream       string
	base, minimum    int64
	prorate          bool
	hasMonthlyCharge bool
	effectiveFrom    time.Time
	from, until      time.Time
}

// feeUsage is a month's usage charge per stream and per card.
type feeUsage struct{ byStream, byCard map[string]int64 }

func newFeeUsage() feeUsage {
	return feeUsage{byStream: map[string]int64{}, byCard: map[string]int64{}}
}

func (u feeUsage) add(stream, card string, amount int64) {
	u.byStream[stream] += amount
	if card != "" {
		u.byCard[card] += amount
	}
}

// feeSegments returns the cards of one stream, ordered by effective_from,
// that charge fees for the month from at until stop (ADR-20261019100332).
// The card in force at the fee instant charges: a whole month when it does
// not prorate (ADR-1316), otherwise the minutes until the next card. A later
// card in the month charges only when it prorates, for the minutes it is in
// force. stop is the month's end, or when the consumer stopped.
func feeSegments(ordered []feeCard, at, stop time.Time) []feeCard {
	end := MonthStart(at).AddDate(0, 1, 0)
	if stop.IsZero() || stop.After(end) {
		stop = end
	}
	current := -1
	for i, card := range ordered {
		if !card.effectiveFrom.After(at) {
			current = i
		}
	}
	var out []feeCard
	for i := max(current, 0); i < len(ordered); i++ {
		card := ordered[i]
		if i != current && !card.prorate {
			continue
		}
		card.from, card.until = at, stop
		if card.effectiveFrom.After(at) {
			card.from = card.effectiveFrom
		}
		if i+1 < len(ordered) && ordered[i+1].effectiveFrom.Before(card.until) {
			card.until = ordered[i+1].effectiveFrom
		}
		if !card.prorate {
			card.until = end
		}
		if card.until.After(card.from) {
			out = append(out, card)
		}
	}
	return out
}

// monthlyStatementFees returns what a revision bills in fees beyond its
// finalized revisions (ADR-1316). A base fee is billed once; a minimum
// top-up is the minimum less the usage it covers, never below zero,
// adjusted by what earlier revisions billed. A card that does not prorate
// charges both in full against its stream's usage; a prorating card scales
// both to its share of the month (ADR-1325, ADR-20261019100332) and counts
// only the usage it priced.
//
// finalized is the usage finalized revisions billed. A top-up falls only by
// the usage billed since, so a share that later shrinks (a card taking over
// or a revocation after the minimum was finalized) never reduces it and a
// revision never goes negative (ADR-20261019180000).
func monthlyStatementFees(cards []feeCard, usage, finalized feeUsage, billed map[state.APIConsumerStatementFee]int64) []state.APIConsumerStatementFee {
	var fees []state.APIConsumerStatementFee
	for _, card := range cards {
		if !card.hasMonthlyCharge {
			continue
		}
		base, minimum := card.base, card.minimum
		covered, coveredBefore := usage.byStream[card.stream], finalized.byStream[card.stream]
		if card.prorate {
			base, minimum = ProrateMonthlyFeeBetween(base, card.from, card.until), ProrateMonthlyFeeBetween(minimum, card.from, card.until)
			covered, coveredBefore = usage.byCard[card.id], finalized.byCard[card.id]
		}
		baseKey := state.APIConsumerStatementFee{Kind: state.StatementFeeBase, RateCardID: card.id}
		if amount := base - billed[baseKey]; amount > 0 {
			baseKey.AmountMillicents = amount
			fees = append(fees, baseKey)
		}
		minimumKey := state.APIConsumerStatementFee{Kind: state.StatementFeeMinimum, RateCardID: card.id}
		owed := max(0, minimum-covered)
		if topUp := max(owed-billed[minimumKey], -max(0, covered-coveredBefore)); topUp != 0 {
			minimumKey.AmountMillicents = topUp
			fees = append(fees, minimumKey)
		}
	}
	return fees
}

// billedFees sums the fees each finalized revision billed, by kind and card.
func billedFees(fees ...[]state.APIConsumerStatementFee) map[state.APIConsumerStatementFee]int64 {
	billed := map[state.APIConsumerStatementFee]int64{}
	for _, list := range fees {
		for _, fee := range list {
			billed[state.APIConsumerStatementFee{Kind: fee.Kind, RateCardID: fee.RateCardID}] += fee.AmountMillicents
		}
	}
	return billed
}

// APIConsumerStatementFees returns what a revision of a whole-month period
// bills in fees beyond its finalized revisions (ADR-1316). Each price
// stream's card in force at the fee instant (ADR-1317) charges its own, and
// prorating cards that take over during the month charge their share
// (ADR-20261019100332).
//
// delta is what this revision bills for usage, so late usage lowers the
// top-up it adds. A trial running at the fee instant waives every fee
// (ADR-1314). opts.EndedAt, a revocation, ends prorating cards' share
// unless a finalized revision of the month was priced before it.
func APIConsumerStatementFees(cards []state.APIConsumerRateCard, start, end, consumerCreated time.Time, opts QuoteOptions,
	revisions []state.APIConsumerUsageStatement, delta []APIConsumerUsageChargeBucket) []state.APIConsumerStatementFee {
	if !IsCalendarMonth(start, end) {
		return nil
	}
	at, ok := FeeInstant(start, consumerCreated)
	// A trial given at creation starts at the next minute (ADR-1314).
	if !ok || opts.inTrial(at) || opts.inTrial(at.Add(time.Minute)) {
		return nil
	}
	usage, finalized := newFeeUsage(), newFeeUsage()
	var finalizedFees [][]state.APIConsumerStatementFee
	closeAt := opts.EndedAt
	for _, statement := range revisions {
		if statement.Status != state.APIConsumerUsageStatementFinalized {
			continue
		}
		for _, bucket := range statement.Buckets {
			usage.add(bucket.Unit, bucket.RateCardID, bucket.AmountMillicents)
			finalized.add(bucket.Unit, bucket.RateCardID, bucket.AmountMillicents)
		}
		finalizedFees = append(finalizedFees, statement.Fees)
		// Fees priced before the revocation and then finalized cover the
		// whole month (ADR-20261019180000).
		if !closeAt.IsZero() && statement.AsOf.Before(closeAt) {
			closeAt = time.Time{}
		}
	}
	for _, bucket := range delta {
		usage.add(bucket.Unit, bucket.RateCardID, bucket.AmountMillicents)
	}
	_, streams := UnitStreams(cards)
	var charging []feeCard
	for _, stream := range streams {
		ordered := make([]feeCard, 0, len(stream))
		for _, card := range stream {
			ordered = append(ordered, feeCard{id: card.ID, stream: bucketUnit(StreamUnit(card.Unit)),
				base: card.BaseFeeMillicentsPerMonth, minimum: card.MinimumMillicentsPerMonth, effectiveFrom: card.EffectiveFrom,
				prorate: card.ProrateMonthlyFees, hasMonthlyCharge: card.MonthlyFees()})
		}
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].effectiveFrom.Before(ordered[j].effectiveFrom) })
		charging = append(charging, feeSegments(ordered, at, closeAt)...)
	}
	sort.SliceStable(charging, func(i, j int) bool { return charging[i].stream < charging[j].stream })
	return monthlyStatementFees(charging, usage, finalized, billedFees(finalizedFees...))
}
