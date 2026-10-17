package billing

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1316 — the base fee bills once per month; the minimum top-up shrinks
// as late usage covers it, so the revision total never falls.
func TestAPIConsumerStatementFees(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	card := state.APIConsumerRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest,
		PriceMillicentsPerUnit: 100, BaseFeeMillicentsPerMonth: 500_000, MinimumMillicentsPerMonth: 1_000_000, EffectiveFrom: start}
	cards := []state.APIConsumerRateCard{card}
	created := start.AddDate(0, -1, 0)

	first := APIConsumerStatementFees(cards, start, end, created, QuoteOptions{}, nil, usageDelta(300))
	want := []state.APIConsumerStatementFee{
		{Kind: state.StatementFeeBase, RateCardID: card.ID, AmountMillicents: 500_000},
		{Kind: state.StatementFeeMinimum, RateCardID: card.ID, AmountMillicents: 999_700},
	}
	if !slices.Equal(first, want) {
		t.Fatalf("first fees = %+v", first)
	}
	finalized := []state.APIConsumerUsageStatement{{Status: state.APIConsumerUsageStatementFinalized, Fees: first,
		Buckets: []state.APIConsumerUsageStatementBucket{{AmountMillicents: 300}}}}
	late := APIConsumerStatementFees(cards, start, end, created, QuoteOptions{}, finalized, usageDelta(200))
	if !slices.Equal(late, []state.APIConsumerStatementFee{{Kind: state.StatementFeeMinimum, RateCardID: card.ID, AmountMillicents: -200}}) {
		t.Fatalf("late fees = %+v; want the top-up to shrink by the late usage", late)
	}
	if over := APIConsumerStatementFees(cards, start, end, created, QuoteOptions{}, finalized, usageDelta(2_000_000)); over[0].AmountMillicents != -999_700 {
		t.Fatalf("usage past the minimum: fees = %+v", over)
	}

	if fees := APIConsumerStatementFees(cards, start, start.AddDate(0, 0, 7), created, QuoteOptions{}, nil, nil); fees != nil {
		t.Fatalf("a week: fees = %+v", fees)
	}
	if fees := APIConsumerStatementFees(cards, start, end, created, QuoteOptions{TrialStart: start.Add(-time.Hour), TrialEnd: start.Add(time.Hour)}, nil, nil); fees != nil {
		t.Fatalf("trial at the month start: fees = %+v", fees)
	}
	if fees := APIConsumerStatementFees(cards, start, end, end.Add(time.Hour), QuoteOptions{}, nil, nil); fees != nil {
		t.Fatalf("consumer created after the month: fees = %+v", fees)
	}
	later := card
	later.ID, later.EffectiveFrom, later.BaseFeeMillicentsPerMonth = uuid.NewString(), start.AddDate(0, 0, 10), 900_000
	midMonth := start.AddDate(0, 0, 15)
	if fees := APIConsumerStatementFees([]state.APIConsumerRateCard{card, later}, start, end, midMonth, QuoteOptions{}, nil, nil); fees[0].RateCardID != later.ID ||
		fees[0].AmountMillicents != 900_000 {
		t.Fatalf("consumer created mid-month: fees = %+v; want the card in force at creation", fees)
	}
	if !MonthlyFeeCardEffectiveIn(cards, start, end) {
		t.Fatal("fee card not reported in force")
	}
}

func usageDelta(amount int64) []APIConsumerUsageChargeBucket {
	return []APIConsumerUsageChargeBucket{{AmountMillicents: amount}}
}
