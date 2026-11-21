package billing

// Boundaries and guards of API consumer pricing that the weekly mutation run
// (make test-mutation-billing) showed no test noticed changing. Overflow
// guards are checked at the exact edge: a total of exactly MaxInt64 is
// representable and must be accepted; one more is refused.

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMultiplyMillicentsGuards(t *testing.T) {
	if _, err := multiplyMillicents(-1, 5); err == nil {
		t.Fatal("negative units accepted")
	}
	if _, err := multiplyMillicents(5, -1); err == nil {
		t.Fatal("negative price accepted")
	}
	// maxInt64/2 units at 2 is maxInt64-1: representable.
	if got, err := multiplyMillicents(maxInt64/2, 2); err != nil || got != maxInt64-1 {
		t.Fatalf("edge product = %d, %v", got, err)
	}
	if _, err := multiplyMillicents(maxInt64/2+1, 2); err == nil {
		t.Fatal("overflowing product accepted")
	}
}

func TestIsUpperASCIICurrencyBounds(t *testing.T) {
	for _, currency := range []string{"AAA", "ZZZ"} {
		if !isUpperASCIICurrency(currency) {
			t.Fatalf("%s rejected", currency)
		}
	}
	for _, currency := range []string{"@AA", "AA["} {
		if isUpperASCIICurrency(currency) {
			t.Fatalf("%s accepted", currency)
		}
	}
}

func TestQuoteAddTotalsAtTheEdge(t *testing.T) {
	priced := APIConsumerUsageChargeBucket{RateCardID: "c", BillableUnits: 5, AmountMillicents: 5}
	q := APIConsumerUsageQuote{BillableUnits: maxInt64 - 5, AmountMillicents: maxInt64 - 5}
	if err := q.add(priced); err != nil || q.BillableUnits != maxInt64 || q.AmountMillicents != maxInt64 {
		t.Fatalf("edge totals = %+v, %v", q, err)
	}
	q = APIConsumerUsageQuote{AmountMillicents: maxInt64 - 1}
	if err := q.add(priced); err == nil {
		t.Fatal("charge overflow accepted")
	}

	unpriced := APIConsumerUsageChargeBucket{BillableUnits: 5}
	q = APIConsumerUsageQuote{UnpricedUnits: maxInt64 - 5}
	if err := q.add(unpriced); err != nil || q.UnpricedUnits != maxInt64 {
		t.Fatalf("edge unpriced = %+v, %v", q, err)
	}

	// A re-rated adjustment may be negative; the floor is -MaxInt64.
	adjustment := APIConsumerUsageChargeBucket{RateCardID: "c", AmountMillicents: -5}
	q = APIConsumerUsageQuote{AmountMillicents: -maxInt64 + 5}
	if err := q.add(adjustment); err != nil || q.AmountMillicents != -maxInt64 {
		t.Fatalf("edge negative = %+v, %v", q, err)
	}
}

func TestMonthUnitTotalsGuards(t *testing.T) {
	minute := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	totals, err := monthUnitTotals([]state.APIConsumerUsageBucket{
		{WindowStart: minute, BillableUnits: 0},
		{WindowStart: minute.Add(time.Minute), BillableUnits: maxInt64 - 5},
		{WindowStart: minute.Add(2 * time.Minute), BillableUnits: 5},
	}, QuoteOptions{})
	if err != nil || totals[minute] != maxInt64 {
		t.Fatalf("edge month total = %v, %v", totals, err)
	}
	if _, err := monthUnitTotals([]state.APIConsumerUsageBucket{{WindowStart: minute, BillableUnits: -1}}, QuoteOptions{}); err == nil {
		t.Fatal("negative units accepted")
	}
}

func TestQuoteAPIConsumerUsageEdges(t *testing.T) {
	minute := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	free := state.APIConsumerRateCard{ID: "free", Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest, EffectiveFrom: minute}

	// No card and no usage is not a priced quote.
	if quote, err := QuoteAPIConsumerUsage(nil, nil); err != nil || quote.Priced {
		t.Fatalf("empty quote = %+v, %v", quote, err)
	}
	// Usage before the first card leaves the quote unpriced.
	early := []state.APIConsumerUsageBucket{{WindowStart: minute.Add(-time.Minute), BillableUnits: 1}, {WindowStart: minute, BillableUnits: 1}}
	if quote, err := QuoteAPIConsumerUsage([]state.APIConsumerRateCard{free}, early); err != nil || quote.Priced {
		t.Fatalf("partly unpriced quote = %+v, %v", quote, err)
	}
	// A zero-unit minute is valid, and a month of exactly MaxInt64 units fits.
	edge := []state.APIConsumerUsageBucket{
		{WindowStart: minute, BillableUnits: 0},
		{WindowStart: minute.Add(time.Minute), BillableUnits: maxInt64 - 5},
		{WindowStart: minute.Add(2 * time.Minute), BillableUnits: 5},
	}
	if quote, err := QuoteAPIConsumerUsage([]state.APIConsumerRateCard{free}, edge); err != nil || quote.BillableUnits != maxInt64 || !quote.Priced {
		t.Fatalf("edge quote = %+v, %v", quote, err)
	}
}

func TestPriceMinuteVolumeStepLock(t *testing.T) {
	ten := int64(10)
	card := state.APIConsumerRateCard{ID: "vol", Currency: "EUR", TierMode: state.RateCardTierModeVolume,
		Tiers: []state.APIConsumerRateCardTier{{UpTo: &ten, PriceMillicentsPerUnit: 100}, {PriceMillicentsPerUnit: 50}}}
	bucket := APIConsumerUsageChargeBucket{BillableUnits: 4}
	// A month of 20 units reaches the second step, but a locked step wins.
	cases := []struct {
		name  string
		steps map[string]int
		want  int64
	}{
		{"locked first step", map[string]int{"vol": 0}, 400},
		{"unlocked", nil, 200},
		{"locked past the ladder", map[string]int{"vol": 2}, 200},
		{"locked below zero", map[string]int{"vol": -1}, 200},
	}
	for _, tc := range cases {
		got, err := priceMinute(card, bucket, 0, 20, QuoteOptions{VolumeSteps: tc.steps})
		if err != nil || got.AmountMillicents != tc.want {
			t.Fatalf("%s: amount = %d, %v; want %d", tc.name, got.AmountMillicents, err, tc.want)
		}
	}

	// Volume mode without a ladder prices flat.
	flat := state.APIConsumerRateCard{ID: "flat", Currency: "EUR", TierMode: state.RateCardTierModeVolume, PriceMillicentsPerUnit: 7}
	if got, err := priceMinute(flat, bucket, 0, 4, QuoteOptions{}); err != nil || got.AmountMillicents != 28 {
		t.Fatalf("flat volume card = %d, %v; want 28", got.AmountMillicents, err)
	}
}

func TestPriceMinuteGraduatedOverflowEdge(t *testing.T) {
	one := int64(1)
	edge := state.APIConsumerRateCard{ID: "g", Currency: "EUR",
		Tiers: []state.APIConsumerRateCardTier{{UpTo: &one, PriceMillicentsPerUnit: maxInt64 - 5}, {PriceMillicentsPerUnit: 5}}}
	if got, err := priceMinute(edge, APIConsumerUsageChargeBucket{BillableUnits: 2}, 0, 2, QuoteOptions{}); err != nil || got.AmountMillicents != maxInt64 {
		t.Fatalf("edge ladder = %d, %v; want MaxInt64", got.AmountMillicents, err)
	}
	huge := state.APIConsumerRateCard{ID: "h", Currency: "EUR", Tiers: []state.APIConsumerRateCardTier{{PriceMillicentsPerUnit: maxInt64}}}
	if _, err := priceMinute(huge, APIConsumerUsageChargeBucket{BillableUnits: 2}, 0, 2, QuoteOptions{}); err == nil {
		t.Fatal("tier product overflow accepted")
	}
}

func TestQuotePlatformTenantUsageEdges(t *testing.T) {
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	tenantCard := state.PlatformTenantRateCard{ID: "tenant", Currency: "EUR", Unit: state.PlatformTenantRateCardUnitRequest,
		PriceMillicentsPerUnit: 1, EffectiveFrom: start}

	// Totals of exactly MaxInt64 units and millicents fit.
	edge := []state.APIConsumerUsageBucket{{WindowStart: start, BillableUnits: maxInt64 - 5}, {WindowStart: start.Add(time.Minute), BillableUnits: 5}}
	quote, err := QuotePlatformTenantUsage(nil, []state.PlatformTenantRateCard{tenantCard}, edge)
	if err != nil || quote.BillableUnits != maxInt64 || quote.AmountMillicents != maxInt64 {
		t.Fatalf("edge tenant quote = %+v, %v", quote, err)
	}

	// A trial prices tenant-card minutes at zero.
	trial := QuoteOptions{TrialStart: start, TrialEnd: start.Add(time.Hour)}
	usage := []state.APIConsumerUsageBucket{{WindowStart: start, BillableUnits: 10}}
	if quote, err := QuotePlatformTenantUsage(nil, []state.PlatformTenantRateCard{tenantCard}, usage, trial); err != nil || quote.AmountMillicents != 0 {
		t.Fatalf("trial tenant quote = %+v, %v; want nothing charged", quote, err)
	}

	// Minutes before the tenant card fall back to app cards in order.
	later := tenantCard
	later.EffectiveFrom = start.Add(10 * time.Minute)
	appCard := state.APIConsumerRateCard{ID: "app", Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest, PriceMillicentsPerUnit: 3, EffectiveFrom: start}
	fallback := []state.APIConsumerUsageBucket{{WindowStart: start, BillableUnits: 1}, {WindowStart: start.Add(time.Minute), BillableUnits: 2}}
	quote, err = QuotePlatformTenantUsage([]state.APIConsumerRateCard{appCard}, []state.PlatformTenantRateCard{later}, fallback)
	if err != nil || len(quote.Buckets) != 2 || quote.Buckets[0].AmountMillicents != 3 || quote.Buckets[1].AmountMillicents != 6 {
		t.Fatalf("fallback quote = %+v, %v; want 3 then 6", quote, err)
	}
	// A trial frees app-card fallback minutes too.
	quote, err = QuotePlatformTenantUsage([]state.APIConsumerRateCard{appCard}, []state.PlatformTenantRateCard{later}, fallback, trial)
	if err != nil || quote.AmountMillicents != 0 {
		t.Fatalf("trial fallback quote = %+v, %v; want nothing charged", quote, err)
	}

	// Without any card the units are unpriced, up to exactly MaxInt64.
	quote, err = QuotePlatformTenantUsage(nil, []state.PlatformTenantRateCard{later}, edge)
	if err != nil || quote.UnpricedUnits != maxInt64 || quote.Priced {
		t.Fatalf("unpriced tenant quote = %+v, %v", quote, err)
	}
}
