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

// adr: 1318 — each reported meter follows its effective rate-card history.
func TestUnpricedConsumerModelUsageRespectsMeterHistory(t *testing.T) {
	start := allowanceStart
	active := start.Add(time.Minute)
	retired := active.Add(time.Minute)
	modelCard := unitCard("app:tokens", active, 2)
	modelCard.ModelPrices = map[string]int64{"premium": 5}
	retiredCard := unitCard("app:tokens", retired, 3)
	standalone := unitCard("app:tokens__custom", active, 1)
	cards := []state.APIConsumerRateCard{retiredCard, standalone, modelCard}
	meters := map[string][]state.APIConsumerMeterUsageBucket{
		"tokens__preview": {{WindowStart: start, Units: 10}, {WindowStart: active, Units: 20}},
		"tokens__premium": {{WindowStart: active, Units: 30}, {WindowStart: retired, Units: 40}},
		"tokens__custom":  {{WindowStart: active, Units: 50}},
		"images__preview": {{WindowStart: active, Units: 60}},
	}
	got := UnpricedConsumerModelUsage(cards, meters, start)
	if len(got) != 2 ||
		got[0].Model != "preview" || got[0].BillableUnits != 20 || !got[0].WindowStart.Equal(active) ||
		got[1].Model != "premium" || got[1].BillableUnits != 40 || !got[1].WindowStart.Equal(retired) {
		t.Fatalf("unpriced aliases = %+v", got)
	}
}

// adr: 949 — later usage belongs in a separate adjustment after finalization.
func TestConsumerModelWaiverPreservesFinalizedCoverage(t *testing.T) {
	start := allowanceStart
	card := unitCard("app:tokens", start, 1000)
	card.ModelPrices = map[string]int64{"premium": 5000}
	card.IncludedUnitsPerMonth = 10
	cards := []state.APIConsumerRateCard{card}
	usage := []state.APIConsumerUsageBucket{usageAt(start, 2)}
	meters := map[string][]state.APIConsumerMeterUsageBucket{
		"tokens__premium": {{WindowStart: start, Units: 15}},
		"tokens__preview": {{WindowStart: start, Units: 17}},
	}
	opts := QuoteOptions{ModelWaivers: []state.APIConsumerUsageWaiver{{
		Unit: card.Unit, Model: "preview", Minutes: []state.APIConsumerUsageWaiverMinute{{WindowStart: start, Units: 17}},
	}}}
	quote, err := QuoteConsumerUsage(cards, usage, ConsumerUsageInputs{Meters: meters}, start, opts)
	if err != nil || !quote.Priced || quote.BillableUnits != 32 || quote.AmountMillicents != 25000 ||
		len(quote.Buckets) != 2 || quote.Buckets[1].WaivedUnits != 17 {
		t.Fatalf("waived quote = %+v, err = %v", quote, err)
	}
	statement := finalizedFrom(quote)
	statement.Currency, statement.AmountMillicents = quote.Currency, quote.AmountMillicents
	for i, bucket := range quote.Buckets {
		statement.Buckets[i].Model = bucket.Model
		statement.Buckets[i].WaivedUnits = bucket.WaivedUnits
		statement.Buckets[i].PriceMillicentsPerUnit = bucket.PriceMillicentsPerUnit
	}
	invoice, err := AppStatementInvoice(statement, cards)
	if err != nil || invoice.TotalMinor != 25 || len(invoice.Lines) != 2 {
		t.Fatalf("invoice = %+v, err = %v", invoice, err)
	}
	meters["tokens__preview"][0].Units += 3
	quote, err = QuoteConsumerUsage(cards, usage, ConsumerUsageInputs{Meters: meters}, start, opts)
	if err != nil || quote.Priced || quote.UnpricedUnits != 3 {
		t.Fatalf("late quote = %+v, err = %v", quote, err)
	}
	delta, err := APIConsumerStatementDelta(quote, []state.APIConsumerUsageStatement{statement})
	if err != nil || delta.Priced || delta.BillableUnits != 3 || delta.UnpricedUnits != 3 || delta.AmountMillicents != 0 {
		t.Fatalf("late adjustment = %+v, err = %v", delta, err)
	}
}
