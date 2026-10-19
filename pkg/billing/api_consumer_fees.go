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
	end := start.AddDate(0, 1, 0)
	if amount <= 0 || !at.After(start) {
		return amount
	}
	if !at.Before(end) {
		return 0
	}
	total := int64(end.Sub(start) / time.Minute)
	left := int64(end.Sub(at.UTC().Truncate(time.Minute)) / time.Minute)
	scaled := new(big.Int).Mul(big.NewInt(amount), big.NewInt(left))
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

// feeCard is a card in force at a fee instant, reduced to what fees read.
type feeCard struct {
	id, stream       string
	base, minimum    int64
	prorate          bool
	hasMonthlyCharge bool
}

// monthlyStatementFees returns what a revision bills in fees beyond its
// finalized revisions, given each stream's card in force at the fee
// instant and each stream's usage charge for the whole month (ADR-1316).
// A base fee is billed once; a minimum top-up is the minimum less its
// stream's usage, never below zero, adjusted by what earlier revisions
// billed. A prorating card scales both to the month left after at
// (ADR-1325).
func monthlyStatementFees(cards []feeCard, at time.Time, usage map[string]int64,
	billed map[state.APIConsumerStatementFee]int64) []state.APIConsumerStatementFee {
	var fees []state.APIConsumerStatementFee
	for _, card := range cards {
		if !card.hasMonthlyCharge {
			continue
		}
		base, minimum := card.base, card.minimum
		if card.prorate {
			base, minimum = ProrateMonthlyFee(base, at), ProrateMonthlyFee(minimum, at)
		}
		baseKey := state.APIConsumerStatementFee{Kind: state.StatementFeeBase, RateCardID: card.id}
		if amount := base - billed[baseKey]; amount > 0 {
			baseKey.AmountMillicents = amount
			fees = append(fees, baseKey)
		}
		minimumKey := state.APIConsumerStatementFee{Kind: state.StatementFeeMinimum, RateCardID: card.id}
		owed := max(0, minimum-usage[card.stream])
		if topUp := owed - billed[minimumKey]; topUp != 0 {
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
// stream's card in force at the fee instant (ADR-1317) charges its own.
//
// delta is what this revision bills for usage, so late usage lowers the
// top-up it adds. A trial running at the fee instant waives every fee
// (ADR-1314).
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
	usage := map[string]int64{}
	var finalizedFees [][]state.APIConsumerStatementFee
	for _, statement := range revisions {
		if statement.Status != state.APIConsumerUsageStatementFinalized {
			continue
		}
		for _, bucket := range statement.Buckets {
			usage[bucket.Unit] += bucket.AmountMillicents
		}
		finalizedFees = append(finalizedFees, statement.Fees)
	}
	for _, bucket := range delta {
		usage[bucket.Unit] += bucket.AmountMillicents
	}
	var inForce []feeCard
	for _, card := range streamCardsAt(cards, at) {
		inForce = append(inForce, feeCard{id: card.ID, stream: bucketUnit(StreamUnit(card.Unit)),
			base: card.BaseFeeMillicentsPerMonth, minimum: card.MinimumMillicentsPerMonth,
			prorate: card.ProrateMonthlyFees, hasMonthlyCharge: card.MonthlyFees()})
	}
	return monthlyStatementFees(inForce, at, usage, billedFees(finalizedFees...))
}
