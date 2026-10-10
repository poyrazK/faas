package billing

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

var allowanceStart = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func allowanceCard(from time.Time, price, included int64) state.APIConsumerRateCard {
	return state.APIConsumerRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest,
		PriceMillicentsPerUnit: price, IncludedUnitsPerMonth: included, EffectiveFrom: from}
}

func usageAt(minute time.Time, units int64) state.APIConsumerUsageBucket {
	return state.APIConsumerUsageBucket{WindowStart: minute, BillableUnits: units, RequestCount: units}
}

func chargedUnits(q APIConsumerUsageQuote) []int64 {
	out := make([]int64, 0, len(q.Buckets))
	for _, b := range q.Buckets {
		out = append(out, b.ChargedUnits)
	}
	return out
}

// adr: 971
func TestQuoteAPIConsumerUsageConsumesMonthlyAllowanceInMinuteOrder(t *testing.T) {
	cards := []state.APIConsumerRateCard{allowanceCard(allowanceStart, 10, 5)}
	usage := []state.APIConsumerUsageBucket{
		usageAt(allowanceStart, 3),
		usageAt(allowanceStart.Add(time.Minute), 4),   // 2 free, 2 charged
		usageAt(allowanceStart.Add(2*time.Minute), 6), // all charged
		usageAt(allowanceStart.AddDate(0, 1, 0), 7),   // new month: 5 free, 2 charged
	}
	quote, err := QuoteAPIConsumerUsage(cards, usage)
	if err != nil {
		t.Fatal(err)
	}
	if got := chargedUnits(quote); len(got) != 4 || got[0] != 0 || got[1] != 2 || got[2] != 6 || got[3] != 2 {
		t.Fatalf("charged = %v, want [0 2 6 2]", got)
	}
	if quote.BillableUnits != 20 || quote.AmountMillicents != 100 || !quote.Priced {
		t.Fatalf("quote = %+v, want 20 units and 100 millicents", quote)
	}
}

func TestQuoteAPIConsumerUsageFromCountsEarlierMonthUsage(t *testing.T) {
	cards := []state.APIConsumerRateCard{allowanceCard(allowanceStart, 10, 5)}
	from := allowanceStart.Add(time.Hour)
	usage := []state.APIConsumerUsageBucket{usageAt(allowanceStart, 4), usageAt(from, 3)}
	quote, err := QuoteAPIConsumerUsageFrom(cards, usage, from)
	if err != nil {
		t.Fatal(err)
	}
	if len(quote.Buckets) != 1 || quote.Buckets[0].ChargedUnits != 2 || quote.BillableUnits != 3 || quote.AmountMillicents != 20 {
		t.Fatalf("quote = %+v, want only the reported minute with 1 free and 2 charged units", quote)
	}
}

func TestQuoteAPIConsumerUsageAllowanceFollowsEffectiveCard(t *testing.T) {
	// The month already used 4 units when a card with a larger allowance
	// takes effect; that card's allowance counts the same month total.
	change := allowanceStart.Add(time.Hour)
	cards := []state.APIConsumerRateCard{allowanceCard(allowanceStart, 10, 2), allowanceCard(change, 10, 6)}
	quote, err := QuoteAPIConsumerUsage(cards, []state.APIConsumerUsageBucket{usageAt(allowanceStart, 4), usageAt(change, 4)})
	if err != nil {
		t.Fatal(err)
	}
	if got := chargedUnits(quote); got[0] != 2 || got[1] != 2 {
		t.Fatalf("charged = %v, want [2 2]", got)
	}
}

func TestAPIConsumerStatementDeltaBillsAllowanceLostToLateUsage(t *testing.T) {
	cards := []state.APIConsumerRateCard{allowanceCard(allowanceStart, 10, 5)}
	m0, m1 := allowanceStart, allowanceStart.Add(time.Minute)
	finalizedQuote, err := QuoteAPIConsumerUsage(cards, []state.APIConsumerUsageBucket{usageAt(m0, 3), usageAt(m1, 4)})
	if err != nil {
		t.Fatal(err)
	}
	finalized := state.APIConsumerUsageStatement{Status: state.APIConsumerUsageStatementFinalized}
	for _, b := range finalizedQuote.Buckets {
		charged := b.ChargedUnits
		finalized.Buckets = append(finalized.Buckets, state.APIConsumerUsageStatementBucket{
			WindowStart: b.WindowStart, BillableUnits: b.BillableUnits, RateCardID: b.RateCardID, ChargedUnits: &charged})
	}
	// Two late units land in the first minute: they are free, but the second
	// minute loses two free units it had received.
	current, err := QuoteAPIConsumerUsage(cards, []state.APIConsumerUsageBucket{usageAt(m0, 5), usageAt(m1, 4)})
	if err != nil {
		t.Fatal(err)
	}
	delta, err := APIConsumerStatementDelta(current, []state.APIConsumerUsageStatement{finalized})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Buckets) != 2 || delta.Buckets[0].BillableUnits != 2 || delta.Buckets[0].ChargedUnits != 0 ||
		delta.Buckets[1].BillableUnits != 0 || delta.Buckets[1].ChargedUnits != 2 {
		t.Fatalf("delta buckets = %+v", delta.Buckets)
	}
	if delta.BillableUnits != 2 || delta.AmountMillicents != 20 || !delta.Priced {
		t.Fatalf("delta = %+v, want 2 new units and 2 newly charged units", delta)
	}
	replay, err := APIConsumerStatementDelta(finalizedQuote, []state.APIConsumerUsageStatement{finalized})
	if err != nil || len(replay.Buckets) != 0 {
		t.Fatalf("unchanged usage delta = %+v err=%v, want empty", replay, err)
	}
}

func TestAPIConsumerStatementDeltaIgnoresDraftsAndReadsLegacyBuckets(t *testing.T) {
	cards := []state.APIConsumerRateCard{allowanceCard(allowanceStart, 10, 0)}
	current, err := QuoteAPIConsumerUsage(cards, []state.APIConsumerUsageBucket{usageAt(allowanceStart, 5)})
	if err != nil {
		t.Fatal(err)
	}
	legacy := state.APIConsumerUsageStatement{Status: state.APIConsumerUsageStatementFinalized, Buckets: []state.APIConsumerUsageStatementBucket{
		{WindowStart: allowanceStart, BillableUnits: 3, RateCardID: cards[0].ID}, // no charged_units: all charged
	}}
	draft := state.APIConsumerUsageStatement{Status: state.APIConsumerUsageStatementDraft, Buckets: []state.APIConsumerUsageStatementBucket{
		{WindowStart: allowanceStart, BillableUnits: 5, RateCardID: cards[0].ID},
	}}
	delta, err := APIConsumerStatementDelta(current, []state.APIConsumerUsageStatement{legacy, draft})
	if err != nil {
		t.Fatal(err)
	}
	if delta.BillableUnits != 2 || delta.AmountMillicents != 20 {
		t.Fatalf("delta = %+v, want 2 units beyond the legacy finalized 3", delta)
	}
}

func TestAPIConsumerStatementDeltaRejectsRegressedUsage(t *testing.T) {
	cards := []state.APIConsumerRateCard{allowanceCard(allowanceStart, 10, 0)}
	charged := int64(3)
	finalized := []state.APIConsumerUsageStatement{{Status: state.APIConsumerUsageStatementFinalized,
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: allowanceStart, BillableUnits: 3, RateCardID: cards[0].ID, ChargedUnits: &charged}}}}
	for name, usage := range map[string][]state.APIConsumerUsageBucket{
		"fewer units":    {usageAt(allowanceStart, 2)},
		"minute missing": {usageAt(allowanceStart.Add(time.Minute), 9)},
	} {
		current, err := QuoteAPIConsumerUsage(cards, usage)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := APIConsumerStatementDelta(current, finalized); !errors.Is(err, ErrAPIConsumerUsageRegressed) {
			t.Errorf("%s: err = %v, want ErrAPIConsumerUsageRegressed", name, err)
		}
	}
}

func TestQuotePlatformTenantUsageRejectsAppAllowances(t *testing.T) {
	cards := []state.APIConsumerRateCard{allowanceCard(allowanceStart, 10, 100)}
	if _, err := QuotePlatformTenantUsage(cards, nil, []state.APIConsumerUsageBucket{usageAt(allowanceStart, 1)}); !errors.Is(err, ErrAPIConsumerAllowanceInTenantStatement) {
		t.Fatalf("err = %v, want ErrAPIConsumerAllowanceInTenantStatement", err)
	}
}
