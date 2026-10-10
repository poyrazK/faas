package billing

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func planCard(plan string, from time.Time, price int64) state.APIConsumerRateCard {
	return state.APIConsumerRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest,
		PriceMillicentsPerUnit: price, PlanID: plan, EffectiveFrom: from}
}

// adr: 974
func TestPlanCardTimelinePricesEachSegmentByItsPlan(t *testing.T) {
	start := allowanceStart
	defaultCard := planCard("", start, 10)
	proOld := planCard("pro", start.Add(-time.Hour), 5)
	proNew := planCard("pro", start.Add(3*time.Hour), 4)
	cards := []state.APIConsumerRateCard{defaultCard, proOld, proNew, planCard("enterprise", start, 1)}
	assignments := []state.APIConsumerPlanAssignment{
		{PlanID: "pro", EffectiveFrom: start.Add(time.Hour)},
		{PlanID: "", EffectiveFrom: start.Add(5 * time.Hour)},
	}
	timeline := PlanCardTimeline(cards, assignments)
	usage := []state.APIConsumerUsageBucket{
		usageAt(start, 1),                  // default: 10
		usageAt(start.Add(2*time.Hour), 1), // pro (old card): 5
		usageAt(start.Add(4*time.Hour), 1), // pro (new card): 4
		usageAt(start.Add(6*time.Hour), 1), // back to default: 10
	}
	quote, err := QuoteAPIConsumerUsage(timeline, usage)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{defaultCard.ID, proOld.ID, proNew.ID, defaultCard.ID}
	for i, bucket := range quote.Buckets {
		if bucket.RateCardID != want[i] {
			t.Fatalf("minute %d priced by %s, want %s", i, bucket.RateCardID, want[i])
		}
	}
	if quote.AmountMillicents != 29 {
		t.Fatalf("amount = %d, want 29", quote.AmountMillicents)
	}
}

func TestPlanCardTimelineWithoutAssignmentsUsesDefaultCards(t *testing.T) {
	cards := []state.APIConsumerRateCard{planCard("", allowanceStart, 10), planCard("pro", allowanceStart, 1)}
	timeline := PlanCardTimeline(cards, nil)
	if len(timeline) != 1 || timeline[0].PlanID != "" {
		t.Fatalf("timeline = %+v, want only the default card", timeline)
	}
	if PlanPricedAt(cards, "pro", allowanceStart.Add(-time.Minute)) || !PlanPricedAt(cards, "pro", allowanceStart) {
		t.Fatal("PlanPricedAt must require a card in force at the minute")
	}
}

func TestQuotePlatformTenantUsageRejectsPlanCards(t *testing.T) {
	if _, err := QuotePlatformTenantUsage([]state.APIConsumerRateCard{planCard("pro", allowanceStart, 1)}, nil,
		[]state.APIConsumerUsageBucket{usageAt(allowanceStart, 1)}); err == nil {
		t.Fatal("tenant statements must not price apps that use consumer plans")
	}
}
