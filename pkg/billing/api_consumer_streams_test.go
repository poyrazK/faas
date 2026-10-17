package billing

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func unitCard(unit string, from time.Time, price int64) state.APIConsumerRateCard {
	return state.APIConsumerRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: unit, PriceMillicentsPerUnit: price, EffectiveFrom: from}
}

// adr: 1317, 1318 — cards of different units price the same minutes side by
// side, each stream from its first card; a minute no stream prices is one
// unpriced request bucket; app meters price the app's reported units.
func TestQuoteConsumerUsageStreams(t *testing.T) {
	m0 := allowanceStart
	m1, m2 := m0.Add(time.Minute), m0.Add(2*time.Minute)
	cards := []state.APIConsumerRateCard{
		unitCard(state.APIConsumerRateCardUnitRequest, m1, 10),
		unitCard(state.APIConsumerRateCardUnitComputeSecond, m2, 100),
		unitCard("app:tokens", m2, 1),
	}
	usage := []state.APIConsumerUsageBucket{
		{WindowStart: m0, RequestCount: 4, BillableUnits: 4},
		{WindowStart: m1, RequestCount: 3, BillableUnits: 3, ComputeMS: 5000},
		{WindowStart: m2, RequestCount: 2, BillableUnits: 2, ComputeMS: 2600},
	}
	meters := map[string][]state.APIConsumerMeterUsageBucket{"tokens": {{WindowStart: m2, Meter: "tokens", Units: 1500}}}
	quote, err := QuoteConsumerUsage(cards, usage, ConsumerUsageInputs{Meters: meters}, time.Time{}, QuoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		minute time.Time
		unit   string
		units  int64
		amount int64
	}
	want := []row{
		{m0, "", 4, 0},                 // before every stream: unpriced
		{m1, "", 3, 30},                // requests only
		{m2, "", 2, 20},                // requests
		{m2, "app:tokens", 1500, 1500}, // reported tokens
		{m2, "compute_second", 3, 300}, // 2.6 s rounds to 3
	}
	if len(quote.Buckets) != len(want) {
		t.Fatalf("buckets = %+v", quote.Buckets)
	}
	for i, w := range want {
		b := quote.Buckets[i]
		if !b.WindowStart.Equal(w.minute) || b.Unit != w.unit || b.BillableUnits != w.units || b.AmountMillicents != w.amount {
			t.Fatalf("bucket %d = %+v, want %+v", i, b, w)
		}
	}
	if quote.UnpricedUnits != 4 || quote.Priced || quote.AmountMillicents != 1850 {
		t.Fatalf("quote totals = %+v", quote)
	}

	billed := finalizedFrom(quote)
	more := append([]state.APIConsumerUsageBucket(nil), usage...)
	more[2].ComputeMS = 4000
	current, err := QuoteConsumerUsage(cards, more, ConsumerUsageInputs{Meters: meters}, time.Time{}, QuoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	delta, err := APIConsumerStatementDelta(current, []state.APIConsumerUsageStatement{billed})
	if err != nil || len(delta.Buckets) != 1 || delta.Buckets[0].Unit != "compute_second" || delta.AmountMillicents != 100 {
		t.Fatalf("delta = %+v, %v; want one more compute second", delta, err)
	}
}
