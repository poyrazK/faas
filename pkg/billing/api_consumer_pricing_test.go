package billing

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 120 — customer API consumer metering, pricing, and monetization foundation.

func TestQuoteAPIConsumerUsageAppliesVersionedCards(t *testing.T) {
	first := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	second := first.Add(2 * time.Minute)
	cards := []state.APIConsumerRateCard{
		{ID: uuid.NewString(), Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest, PriceMillicentsPerUnit: 100, EffectiveFrom: second},
		{ID: uuid.NewString(), Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest, PriceMillicentsPerUnit: 50, EffectiveFrom: first},
	}
	usage := []state.APIConsumerUsageBucket{
		{WindowStart: first, BillableUnits: 3},
		{WindowStart: first.Add(time.Minute), BillableUnits: 2},
		{WindowStart: second, BillableUnits: 4},
	}
	quote, err := QuoteAPIConsumerUsage(cards, usage)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if !quote.Priced || quote.Currency != "EUR" || quote.BillableUnits != 9 || quote.UnpricedUnits != 0 || quote.AmountMillicents != 650 {
		t.Fatalf("quote = %+v", quote)
	}
	if len(quote.Buckets) != 3 || quote.Buckets[0].AmountMillicents != 150 || quote.Buckets[2].AmountMillicents != 400 {
		t.Fatalf("priced buckets = %+v", quote.Buckets)
	}
}

func TestQuoteAPIConsumerUsageReportsUnpricedUnits(t *testing.T) {
	minute := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	quote, err := QuoteAPIConsumerUsage(nil, []state.APIConsumerUsageBucket{{WindowStart: minute, BillableUnits: 7}})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if quote.Priced || quote.BillableUnits != 7 || quote.UnpricedUnits != 7 || quote.AmountMillicents != 0 || quote.Buckets[0].RateCardID != "" {
		t.Fatalf("unpriced quote = %+v", quote)
	}
}

// A rate card is only priced with a three-letter upper-case ISO currency, a
// non-negative price (zero is a free tier) and an effective_from on a UTC
// minute; anything else is refused rather than quoted.
func TestQuoteAPIConsumerUsageValidatesRateCards(t *testing.T) {
	minute := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	valid := state.APIConsumerRateCard{ID: "a", Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest,
		PriceMillicentsPerUnit: 5, EffectiveFrom: minute}
	for _, tc := range []struct {
		name   string
		edit   func(*state.APIConsumerRateCard)
		wantOK bool
	}{
		{"valid", func(*state.APIConsumerRateCard) {}, true},
		{"zero price is a free tier", func(c *state.APIConsumerRateCard) { c.PriceMillicentsPerUnit = 0 }, true},
		{"negative price", func(c *state.APIConsumerRateCard) { c.PriceMillicentsPerUnit = -1 }, false},
		{"empty currency", func(c *state.APIConsumerRateCard) { c.Currency = "" }, false},
		{"four-letter currency", func(c *state.APIConsumerRateCard) { c.Currency = "EURO" }, false},
		{"lower-case currency", func(c *state.APIConsumerRateCard) { c.Currency = "eur" }, false},
		{"zero effective_from", func(c *state.APIConsumerRateCard) { c.EffectiveFrom = time.Time{} }, false},
		{"effective_from mid-minute", func(c *state.APIConsumerRateCard) { c.EffectiveFrom = minute.Add(30 * time.Second) }, false},
	} {
		card := valid
		tc.edit(&card)
		_, err := QuoteAPIConsumerUsage([]state.APIConsumerRateCard{card}, nil)
		if (err == nil) != tc.wantOK {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.wantOK)
		}
	}
}

func TestQuoteAPIConsumerUsageRejectsCurrencyChangesAndOverflow(t *testing.T) {
	minute := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	_, err := QuoteAPIConsumerUsage([]state.APIConsumerRateCard{
		{ID: "a", Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest, EffectiveFrom: minute},
		{ID: "b", Currency: "USD", Unit: state.APIConsumerRateCardUnitRequest, EffectiveFrom: minute.Add(time.Minute)},
	}, nil)
	if err == nil {
		t.Fatal("multiple currencies: expected error")
	}
	_, err = QuoteAPIConsumerUsage([]state.APIConsumerRateCard{
		{ID: "a", Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest, PriceMillicentsPerUnit: maxInt64, EffectiveFrom: minute},
	}, []state.APIConsumerUsageBucket{{WindowStart: minute, BillableUnits: 2}})
	if err == nil {
		t.Fatal("charge overflow: expected error")
	}
}

func TestQuotePlatformTenantUsageOverridesAppPriceAndFallsBackBeforeEffectiveMinute(t *testing.T) {
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	appCardID, tenantCardID := uuid.NewString(), uuid.NewString()
	appCards := []state.APIConsumerRateCard{{ID: appCardID, Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest,
		PriceMillicentsPerUnit: 10, EffectiveFrom: start}}
	tenantCards := []state.PlatformTenantRateCard{{ID: tenantCardID, Currency: "EUR", Unit: state.PlatformTenantRateCardUnitRequest,
		PriceMillicentsPerUnit: 25, EffectiveFrom: start.Add(time.Minute)}}
	usage := []state.APIConsumerUsageBucket{
		{WindowStart: start.Add(time.Minute), BillableUnits: 2},
		{WindowStart: start, BillableUnits: 3},
	}
	quote, err := QuotePlatformTenantUsage(appCards, tenantCards, usage)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if quote.Currency != "EUR" || quote.BillableUnits != 5 || quote.AmountMillicents != 80 || quote.UnpricedUnits != 0 || len(quote.Buckets) != 2 {
		t.Fatalf("quote = %+v", quote)
	}
	if quote.Buckets[0].RateCardID != appCardID || quote.Buckets[0].PlatformTenantRateCardID != "" || quote.Buckets[0].AmountMillicents != 30 {
		t.Fatalf("pre-effective bucket = %+v", quote.Buckets[0])
	}
	if quote.Buckets[1].RateCardID != "" || quote.Buckets[1].PlatformTenantRateCardID != tenantCardID || quote.Buckets[1].AmountMillicents != 50 {
		t.Fatalf("tenant-priced bucket = %+v", quote.Buckets[1])
	}
}

// adr: 1319 — tenant fallback depends on the app card in force in that minute.
func TestQuotePlatformTenantUsageIgnoresModelCardOutsideFallback(t *testing.T) {
	start := allowanceStart
	later := start.Add(time.Minute)
	app := planCard("", start, 10)
	model := planCard("", later, 5)
	model.Unit, model.ModelPrices = "app:tokens", map[string]int64{"premium": 20}
	cards := []state.APIConsumerRateCard{model, app}
	historical := usageAt(start, 2)
	quote, err := QuotePlatformTenantUsage(cards, nil, []state.APIConsumerUsageBucket{historical})
	if err != nil || !quote.Priced || quote.AmountMillicents != 20 || len(quote.Buckets) != 1 || quote.Buckets[0].RateCardID != app.ID {
		t.Fatalf("historical quote = %+v, err = %v", quote, err)
	}
	current := usageAt(later, 1)
	tenant := state.PlatformTenantRateCard{ID: uuid.NewString(), Currency: "EUR", EffectiveFrom: later, PriceMillicentsPerUnit: 25}
	quote, err = QuotePlatformTenantUsage(cards, []state.PlatformTenantRateCard{tenant}, []state.APIConsumerUsageBucket{current, historical})
	if err != nil || !quote.Priced || quote.AmountMillicents != 45 || len(quote.Buckets) != 2 ||
		quote.Buckets[0].RateCardID != app.ID || quote.Buckets[1].PlatformTenantRateCardID != tenant.ID {
		t.Fatalf("mixed tenant/fallback quote = %+v, err = %v", quote, err)
	}
	quote, err = QuotePlatformTenantUsage(cards, nil, []state.APIConsumerUsageBucket{current})
	if err != nil || !quote.Priced || quote.AmountMillicents != 10 || len(quote.Buckets) != 1 || quote.Buckets[0].RateCardID != app.ID {
		t.Fatalf("request fallback alongside an active model rate = %+v, err = %v", quote, err)
	}
	quote, err = QuotePlatformTenantUsage([]state.APIConsumerRateCard{model}, nil, []state.APIConsumerUsageBucket{current})
	if err != nil || quote.Priced || quote.AmountMillicents != 0 || quote.UnpricedUnits != 1 {
		t.Fatalf("request fallback without a request card = %+v, err = %v", quote, err)
	}
}

// Priced (shown on quotes and statements) means every billable unit has a
// price: one unpriced minute, or no usage at all, leaves the quote unpriced.
func TestQuotePlatformTenantUsagePricedOnlyWhenEveryUnitIsPriced(t *testing.T) {
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	tenantCards := []state.PlatformTenantRateCard{{ID: uuid.NewString(), Currency: "EUR", Unit: state.PlatformTenantRateCardUnitRequest,
		PriceMillicentsPerUnit: 25, EffectiveFrom: start.Add(time.Minute)}}
	for _, tc := range []struct {
		name       string
		usage      []state.APIConsumerUsageBucket
		wantPriced bool
	}{
		{"every minute priced", []state.APIConsumerUsageBucket{{WindowStart: start.Add(time.Minute), BillableUnits: 2}}, true},
		{"a minute before any card", []state.APIConsumerUsageBucket{
			{WindowStart: start, BillableUnits: 3}, {WindowStart: start.Add(time.Minute), BillableUnits: 2}}, false},
		{"no usage", nil, false},
		{"only zero-unit minutes", []state.APIConsumerUsageBucket{{WindowStart: start.Add(time.Minute), BillableUnits: 0}}, false},
	} {
		quote, err := QuotePlatformTenantUsage(nil, tenantCards, tc.usage)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if quote.Priced != tc.wantPriced {
			t.Errorf("%s: Priced = %v, want %v (quote %+v)", tc.name, quote.Priced, tc.wantPriced, quote)
		}
	}
}

func TestQuotePlatformTenantUsageAllowsTenantCurrencyToDifferFromUnusedAppCards(t *testing.T) {
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	appCards := []state.APIConsumerRateCard{{ID: uuid.NewString(), Currency: "USD", Unit: state.APIConsumerRateCardUnitRequest,
		PriceMillicentsPerUnit: 10, EffectiveFrom: start}}
	tenantCards := []state.PlatformTenantRateCard{{ID: uuid.NewString(), Currency: "EUR", Unit: state.PlatformTenantRateCardUnitRequest,
		PriceMillicentsPerUnit: 25, EffectiveFrom: start}}
	quote, err := QuotePlatformTenantUsage(appCards, tenantCards, []state.APIConsumerUsageBucket{{WindowStart: start, BillableUnits: 2}})
	if err != nil || quote.Currency != "EUR" || quote.AmountMillicents != 50 {
		t.Fatalf("tenant tariff quote = %+v, %v", quote, err)
	}
}

func TestQuotePlatformTenantUsageRejectsMixedEffectivePricingCurrencies(t *testing.T) {
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	appCards := []state.APIConsumerRateCard{{ID: uuid.NewString(), Currency: "USD", Unit: state.APIConsumerRateCardUnitRequest,
		PriceMillicentsPerUnit: 10, EffectiveFrom: start}}
	tenantCards := []state.PlatformTenantRateCard{{ID: uuid.NewString(), Currency: "EUR", Unit: state.PlatformTenantRateCardUnitRequest,
		PriceMillicentsPerUnit: 25, EffectiveFrom: start.Add(time.Minute)}}
	_, err := QuotePlatformTenantUsage(appCards, tenantCards, []state.APIConsumerUsageBucket{
		{WindowStart: start, BillableUnits: 1}, {WindowStart: start.Add(time.Minute), BillableUnits: 1},
	})
	if !errors.Is(err, ErrMixedAPIConsumerRateCardCurrency) {
		t.Fatalf("mixed effective pricing currencies err=%v", err)
	}
}
