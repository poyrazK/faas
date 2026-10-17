package billing

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// FeeInstant is when a consumer's month starts for fees (ADR-1316): 00:00
// UTC on the 1st, or the consumer's creation if later. ok is false when the
// consumer did not exist in the month.
func FeeInstant(month, consumerCreated time.Time) (time.Time, bool) {
	start := MonthStart(month)
	at := start
	if created := consumerCreated.UTC().Truncate(time.Minute); created.After(at) {
		at = created
	}
	return at, at.Before(start.AddDate(0, 1, 0))
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

// APIConsumerStatementFees returns what a revision of a whole-month period
// bills in fees beyond its finalized revisions (ADR-1316). Each price
// stream's card in force at the fee instant (ADR-1317) charges its own.
//
// A base fee is billed once. A minimum top-up is the card's minimum less
// its stream's usage charge for the month, never below zero; delta is what
// this revision bills for usage, so late usage lowers the top-up it adds. A
// trial running at the fee instant waives every fee (ADR-1314).
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
	billed := map[state.APIConsumerStatementFee]int64{}
	for _, statement := range revisions {
		if statement.Status != state.APIConsumerUsageStatementFinalized {
			continue
		}
		for _, bucket := range statement.Buckets {
			usage[bucket.Unit] += bucket.AmountMillicents
		}
		for _, fee := range statement.Fees {
			billed[state.APIConsumerStatementFee{Kind: fee.Kind, RateCardID: fee.RateCardID}] += fee.AmountMillicents
		}
	}
	for _, bucket := range delta {
		usage[bucket.Unit] += bucket.AmountMillicents
	}
	var fees []state.APIConsumerStatementFee
	for _, card := range streamCardsAt(cards, at) {
		if !card.MonthlyFees() {
			continue
		}
		baseKey := state.APIConsumerStatementFee{Kind: state.StatementFeeBase, RateCardID: card.ID}
		if base := card.BaseFeeMillicentsPerMonth - billed[baseKey]; base > 0 {
			baseKey.AmountMillicents = base
			fees = append(fees, baseKey)
		}
		minimumKey := state.APIConsumerStatementFee{Kind: state.StatementFeeMinimum, RateCardID: card.ID}
		owed := max(0, card.MinimumMillicentsPerMonth-usage[bucketUnit(StreamUnit(card.Unit))])
		if topUp := owed - billed[minimumKey]; topUp != 0 {
			minimumKey.AmountMillicents = topUp
			fees = append(fees, minimumKey)
		}
	}
	return fees
}
