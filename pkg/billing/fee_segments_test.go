package billing

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func feeAmounts(fees []state.APIConsumerStatementFee) map[string]int64 {
	out := map[string]int64{}
	for _, fee := range fees {
		out[fee.RateCardID+"/"+fee.Kind] += fee.AmountMillicents
	}
	return out
}

// adr: 20261019100332 — a prorating card that takes over mid-month charges
// its share, and the card it replaces stops at the change; a card that does
// not prorate keeps charging its whole month.
func TestFeesProrateAcrossCardChanges(t *testing.T) {
	september := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := september.AddDate(0, 1, 0)
	change := september.AddDate(0, 0, 15)
	created := september.AddDate(0, -1, 0)
	card := func(base int64, from time.Time, prorate bool) state.APIConsumerRateCard {
		return state.APIConsumerRateCard{ID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 1,
			BaseFeeMillicentsPerMonth: base, ProrateMonthlyFees: prorate, EffectiveFrom: from}
	}
	for _, tc := range []struct {
		name                   string
		oldProrate, newProrate bool
		wantOld, wantNew       int64
	}{
		{"both prorate", true, true, 1500, 3000},
		{"old whole month, new prorates", false, true, 3000, 3000},
		{"new does not prorate", true, false, 1500, 0},
		{"neither prorates", false, false, 3000, 0},
	} {
		old, upgraded := card(3000, created, tc.oldProrate), card(6000, change, tc.newProrate)
		got := feeAmounts(APIConsumerStatementFees([]state.APIConsumerRateCard{old, upgraded}, september, end, created, QuoteOptions{}, nil, nil))
		if got[old.ID+"/"+state.StatementFeeBase] != tc.wantOld || got[upgraded.ID+"/"+state.StatementFeeBase] != tc.wantNew {
			t.Errorf("%s: fees = %v, want old %d new %d", tc.name, got, tc.wantOld, tc.wantNew)
		}
	}

	// A prorating card's minimum counts only the usage it priced.
	old, upgraded := card(0, created, true), card(0, change, true)
	old.MinimumMillicentsPerMonth, upgraded.MinimumMillicentsPerMonth = 1000, 2000
	delta := []APIConsumerUsageChargeBucket{{RateCardID: old.ID, AmountMillicents: 300}, {RateCardID: upgraded.ID, AmountMillicents: 400}}
	got := feeAmounts(APIConsumerStatementFees([]state.APIConsumerRateCard{old, upgraded}, september, end, created, QuoteOptions{}, nil, delta))
	if got[old.ID+"/"+state.StatementFeeMinimum] != 200 || got[upgraded.ID+"/"+state.StatementFeeMinimum] != 600 {
		t.Fatalf("segment minimums = %v, want 500-300 and 1000-400", got)
	}
}

// adr: 20261019100332 — a consumer revoked mid-month pays prorating fees
// only up to the revocation, unless a revision of the month was finalized
// before it: finalized fees are never reduced.
func TestFeesStopAtRevocation(t *testing.T) {
	september := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := september.AddDate(0, 1, 0)
	created := september.AddDate(0, -1, 0)
	revoked := september.AddDate(0, 0, 10)
	card := state.APIConsumerRateCard{ID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 1,
		BaseFeeMillicentsPerMonth: 3000, ProrateMonthlyFees: true, EffectiveFrom: created}
	cards := []state.APIConsumerRateCard{card}
	base := card.ID + "/" + state.StatementFeeBase

	got := feeAmounts(APIConsumerStatementFees(cards, september, end, created, QuoteOptions{EndedAt: revoked}, nil, nil))
	if got[base] != 1000 {
		t.Fatalf("revoked after a third of the month: base = %d, want 1000", got[base])
	}
	finalizedAt := revoked.AddDate(0, 0, 25)
	after := []state.APIConsumerUsageStatement{{Status: state.APIConsumerUsageStatementFinalized, FinalizedAt: &finalizedAt,
		Fees: []state.APIConsumerStatementFee{{Kind: state.StatementFeeBase, RateCardID: card.ID, AmountMillicents: 1000}}}}
	if got := feeAmounts(APIConsumerStatementFees(cards, september, end, created, QuoteOptions{EndedAt: revoked}, after, nil)); got[base] != 0 {
		t.Fatalf("adjustment after a revocation-prorated revision: base = %d, want nothing more", got[base])
	}
	early := revoked.AddDate(0, 0, -5)
	before := []state.APIConsumerUsageStatement{{Status: state.APIConsumerUsageStatementFinalized, FinalizedAt: &early,
		Fees: []state.APIConsumerStatementFee{{Kind: state.StatementFeeBase, RateCardID: card.ID, AmountMillicents: 3000}}}}
	if got := feeAmounts(APIConsumerStatementFees(cards, september, end, created, QuoteOptions{EndedAt: revoked}, before, nil)); len(got) != 0 {
		t.Fatalf("revision finalized before the revocation: fees = %v, want none (never reduced, never refunded)", got)
	}
	card.ProrateMonthlyFees = false
	if got := feeAmounts(APIConsumerStatementFees([]state.APIConsumerRateCard{card}, september, end, created, QuoteOptions{EndedAt: revoked}, nil, nil)); got[base] != 3000 {
		t.Fatalf("a card that does not prorate: base = %d, want the whole month", got[base])
	}
}
