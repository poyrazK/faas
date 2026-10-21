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

// adr: 20261019100332, 20261019180000 — a consumer revoked mid-month pays
// prorating fees only up to the revocation, unless a finalized revision
// priced the month before it: finalized fees are never reduced.
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
	pricedAfter := revoked.AddDate(0, 0, 25)
	after := []state.APIConsumerUsageStatement{{Status: state.APIConsumerUsageStatementFinalized, AsOf: pricedAfter, FinalizedAt: &pricedAfter,
		Fees: []state.APIConsumerStatementFee{{Kind: state.StatementFeeBase, RateCardID: card.ID, AmountMillicents: 1000}}}}
	if got := feeAmounts(APIConsumerStatementFees(cards, september, end, created, QuoteOptions{EndedAt: revoked}, after, nil)); got[base] != 0 {
		t.Fatalf("adjustment after a revocation-prorated revision: base = %d, want nothing more", got[base])
	}
	// Priced before the revocation, finalized after it: still the whole month.
	early, finalizedLater := revoked.AddDate(0, 0, -5), revoked.AddDate(0, 0, 5)
	before := []state.APIConsumerUsageStatement{{Status: state.APIConsumerUsageStatementFinalized, AsOf: early, FinalizedAt: &finalizedLater,
		Fees: []state.APIConsumerStatementFee{{Kind: state.StatementFeeBase, RateCardID: card.ID, AmountMillicents: 3000}}}}
	if got := feeAmounts(APIConsumerStatementFees(cards, september, end, created, QuoteOptions{EndedAt: revoked}, before, nil)); len(got) != 0 {
		t.Fatalf("revision finalized before the revocation: fees = %v, want none (never reduced, never refunded)", got)
	}
	card.ProrateMonthlyFees = false
	if got := feeAmounts(APIConsumerStatementFees([]state.APIConsumerRateCard{card}, september, end, created, QuoteOptions{EndedAt: revoked}, nil, nil)); got[base] != 3000 {
		t.Fatalf("a card that does not prorate: base = %d, want the whole month", got[base])
	}
}

// adr: 20261019180000 — a finalized minimum top-up falls only by usage
// billed since: a prorating card's share that shrinks after it was
// finalized (a later card, a revocation priced later) never refunds it, so
// a revision never goes negative; late usage still lowers it.
func TestFinalizedMinimumFallsOnlyByNewUsage(t *testing.T) {
	september := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := september.AddDate(0, 1, 0)
	created := september.AddDate(0, -1, 0)
	old := state.APIConsumerRateCard{ID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 1,
		MinimumMillicentsPerMonth: 3000, ProrateMonthlyFees: true, EffectiveFrom: created}
	free := state.APIConsumerRateCard{ID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 1, ProrateMonthlyFees: true,
		EffectiveFrom: september.AddDate(0, 0, 20)}
	pricedAt := september.AddDate(0, 0, 5)
	rev1 := state.APIConsumerUsageStatement{Status: state.APIConsumerUsageStatementFinalized, AsOf: pricedAt, FinalizedAt: &pricedAt,
		Fees: []state.APIConsumerStatementFee{{Kind: state.StatementFeeMinimum, RateCardID: old.ID, AmountMillicents: 3000}}}
	minimum := old.ID + "/" + state.StatementFeeMinimum
	for _, tc := range []struct {
		name  string
		cards []state.APIConsumerRateCard
		opts  QuoteOptions
		delta []APIConsumerUsageChargeBucket
		want  int64
	}{
		{"card change after finalize", []state.APIConsumerRateCard{old, free}, QuoteOptions{}, nil, 0},
		{"revocation priced after finalize", []state.APIConsumerRateCard{old}, QuoteOptions{EndedAt: september.AddDate(0, 0, 15)}, nil, 0},
		{"late usage lowers the top-up", []state.APIConsumerRateCard{old}, QuoteOptions{},
			[]APIConsumerUsageChargeBucket{{RateCardID: old.ID, AmountMillicents: 400}}, -400},
		{"late usage after a card change", []state.APIConsumerRateCard{old, free}, QuoteOptions{},
			[]APIConsumerUsageChargeBucket{{RateCardID: old.ID, AmountMillicents: 400}}, -400},
	} {
		got := feeAmounts(APIConsumerStatementFees(tc.cards, september, end, created, tc.opts, []state.APIConsumerUsageStatement{rev1}, tc.delta))
		if got[minimum] != tc.want {
			t.Errorf("%s: minimum top-up = %d, want %d (fees %v)", tc.name, got[minimum], tc.want, got)
		}
	}

	// The same holds for a platform tenant's month.
	tenantCard := state.PlatformTenantRateCard{ID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 1,
		MinimumMillicentsPerMonth: 3000, ProrateMonthlyFees: true, EffectiveFrom: created}
	tenantFree := state.PlatformTenantRateCard{ID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 1, ProrateMonthlyFees: true,
		EffectiveFrom: september.AddDate(0, 0, 20)}
	month := TenantMonth{Start: september, End: end, TenantCreated: created, TenantCards: []state.PlatformTenantRateCard{tenantCard, tenantFree},
		Previous: []state.PlatformTenantStatement{{Status: state.APIConsumerUsageStatementFinalized, PeriodStart: september, PeriodEnd: end,
			Fees: []state.APIConsumerStatementFee{{Kind: state.StatementFeeMinimum, RateCardID: tenantCard.ID, AmountMillicents: 3000}}}}}
	if got := feeAmounts(tenantStatementFees(month, newFeeUsage())); got[tenantCard.ID+"/"+state.StatementFeeMinimum] != 0 {
		t.Fatalf("tenant card change after finalize: fees = %v, want no negative top-up", got)
	}
}
