package billing

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func upTo(n int64) *int64 { return &n }

// volumeLadder: first 5 free, up to 10 at 100, above that at 10.
func volumeLadder(from time.Time) state.APIConsumerRateCard {
	return state.APIConsumerRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest,
		PriceMillicentsPerUnit: 10, EffectiveFrom: from, Tiers: []state.APIConsumerRateCardTier{
			{UpTo: upTo(5), PriceMillicentsPerUnit: 0},
			{UpTo: upTo(10), PriceMillicentsPerUnit: 100},
			{PriceMillicentsPerUnit: 10},
		}}
}

func finalizedFrom(q APIConsumerUsageQuote) state.APIConsumerUsageStatement {
	out := state.APIConsumerUsageStatement{Status: state.APIConsumerUsageStatementFinalized}
	for _, b := range q.Buckets {
		charged := b.ChargedUnits
		out.Buckets = append(out.Buckets, state.APIConsumerUsageStatementBucket{WindowStart: b.WindowStart, BillableUnits: b.BillableUnits,
			RateCardID: b.RateCardID, ChargedUnits: &charged, TierUnits: slices.Clone(b.TierUnits), AmountMillicents: b.AmountMillicents})
	}
	return out
}

// adr: 951
func TestQuoteSplitsUnitsAcrossGraduatedTiers(t *testing.T) {
	cards := []state.APIConsumerRateCard{volumeLadder(allowanceStart)}
	quote, err := QuoteAPIConsumerUsage(cards, []state.APIConsumerUsageBucket{
		usageAt(allowanceStart, 3),                  // free 3
		usageAt(allowanceStart.Add(time.Minute), 9), // free 2, 5 at 100, 2 at 10
		usageAt(allowanceStart.AddDate(0, 1, 0), 6), // new month: free 5, 1 at 100
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]int64{{3, 0, 0}, {2, 5, 2}, {5, 1, 0}}
	for i, b := range quote.Buckets {
		if !slices.Equal(b.TierUnits, want[i]) {
			t.Fatalf("bucket %d tier units = %v, want %v", i, b.TierUnits, want[i])
		}
	}
	if quote.Buckets[1].AmountMillicents != 520 || quote.Buckets[1].ChargedUnits != 7 || quote.AmountMillicents != 620 {
		t.Fatalf("quote = %+v, want 520 for the second minute and 620 total", quote)
	}
}

func TestStatementDeltaReRatesTiersExactly(t *testing.T) {
	cards := []state.APIConsumerRateCard{volumeLadder(allowanceStart)}
	m0, m1 := allowanceStart, allowanceStart.Add(time.Minute)
	billed, err := QuoteAPIConsumerUsage(cards, []state.APIConsumerUsageBucket{usageAt(m0, 4), usageAt(m1, 8)})
	if err != nil {
		t.Fatal(err)
	}
	// Billed: m0 = 4 free; m1 = 1 free, 5 at 100, 2 at 10 = 520.
	// Four late units in m0 make it 5 free + 3 at 100, which pushes m1 to
	// 2 at 100 and 6 at 10: m1 drops by 260 while m0 rises by 300.
	current, err := QuoteAPIConsumerUsage(cards, []state.APIConsumerUsageBucket{usageAt(m0, 8), usageAt(m1, 8)})
	if err != nil {
		t.Fatal(err)
	}
	delta, err := APIConsumerStatementDelta(current, []state.APIConsumerUsageStatement{finalizedFrom(billed)})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Buckets) != 2 || !slices.Equal(delta.Buckets[0].TierUnits, []int64{1, 3, 0}) || delta.Buckets[0].AmountMillicents != 300 ||
		!slices.Equal(delta.Buckets[1].TierUnits, []int64{-1, -3, 4}) || delta.Buckets[1].AmountMillicents != -260 {
		t.Fatalf("delta buckets = %+v", delta.Buckets)
	}
	full := current.AmountMillicents - billed.AmountMillicents
	if delta.BillableUnits != 4 || delta.AmountMillicents != full || full != 40 {
		t.Fatalf("delta = %+v, want 4 new units costing exactly the 40 the month total grew by", delta)
	}
}

func TestStatementDeltaRefusesToCredit(t *testing.T) {
	// A mid-month card whose cheapest step comes first can make re-rating
	// lower the total; Gregale refuses rather than issuing a credit.
	m0, m1 := allowanceStart, allowanceStart.Add(time.Hour)
	expensiveLater := state.APIConsumerRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest,
		PriceMillicentsPerUnit: 1, EffectiveFrom: m1, Tiers: []state.APIConsumerRateCardTier{
			{UpTo: upTo(2), PriceMillicentsPerUnit: 1000}, {PriceMillicentsPerUnit: 1},
		}}
	cards := []state.APIConsumerRateCard{allowanceCard(m0, 1, 0), expensiveLater}
	billed, err := QuoteAPIConsumerUsage(cards, []state.APIConsumerUsageBucket{usageAt(m0, 1), usageAt(m1, 1)})
	if err != nil {
		t.Fatal(err)
	}
	current, err := QuoteAPIConsumerUsage(cards, []state.APIConsumerUsageBucket{usageAt(m0, 2), usageAt(m1, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := APIConsumerStatementDelta(current, []state.APIConsumerUsageStatement{finalizedFrom(billed)}); !errors.Is(err, ErrAPIConsumerChargeDecreased) {
		t.Fatalf("err = %v, want ErrAPIConsumerChargeDecreased", err)
	}
}

func TestTieredCardPeriodsAreCalendarMonths(t *testing.T) {
	flat := allowanceCard(allowanceStart, 10, 0)
	tiered := volumeLadder(allowanceStart.Add(48 * time.Hour))
	cards := []state.APIConsumerRateCard{flat, tiered}
	if TieredCardEffectiveIn(cards, allowanceStart, allowanceStart.Add(24*time.Hour)) {
		t.Fatal("first day is priced by the flat card only")
	}
	if !TieredCardEffectiveIn(cards, allowanceStart, allowanceStart.Add(72*time.Hour)) {
		t.Fatal("third day is priced by the tiered card")
	}
	if !IsCalendarMonth(allowanceStart, allowanceStart.AddDate(0, 1, 0)) || IsCalendarMonth(allowanceStart, allowanceStart.AddDate(0, 0, 7)) {
		t.Fatal("calendar month detection")
	}
}

func TestValidateRateCardTiers(t *testing.T) {
	for name, tiers := range map[string][]state.APIConsumerRateCardTier{
		"single step":      {{PriceMillicentsPerUnit: 1}},
		"bounded last":     {{UpTo: upTo(5), PriceMillicentsPerUnit: 0}, {UpTo: upTo(9), PriceMillicentsPerUnit: 1}},
		"unbounded middle": {{PriceMillicentsPerUnit: 0}, {PriceMillicentsPerUnit: 1}},
		"not increasing":   {{UpTo: upTo(5), PriceMillicentsPerUnit: 2}, {UpTo: upTo(5), PriceMillicentsPerUnit: 1}, {PriceMillicentsPerUnit: 1}},
		"free later step":  {{UpTo: upTo(5), PriceMillicentsPerUnit: 2}, {PriceMillicentsPerUnit: 0}},
		"negative price":   {{UpTo: upTo(5), PriceMillicentsPerUnit: -1}, {PriceMillicentsPerUnit: 1}},
		"zero first bound": {{UpTo: upTo(0), PriceMillicentsPerUnit: 0}, {PriceMillicentsPerUnit: 1}},
	} {
		if err := state.ValidateAPIConsumerRateCardTiers(tiers); err == nil {
			t.Errorf("%s: accepted %+v", name, tiers)
		}
	}
	if err := state.ValidateAPIConsumerRateCardTiers(volumeLadder(allowanceStart).Tiers); err != nil {
		t.Fatalf("valid ladder rejected: %v", err)
	}
}
