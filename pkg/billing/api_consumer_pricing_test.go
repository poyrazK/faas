package billing

import (
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
