package billing

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func planCard(plan string, from time.Time, price int64) state.APIConsumerRateCard {
	return state.APIConsumerRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest,
		PriceMillicentsPerUnit: price, PlanID: plan, EffectiveFrom: from}
}

// adr: 953
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

// adr: 1317 — an assignment starts each unit's effective card at the boundary.
func TestPlanCardTimelineKeepsParallelRatesAtAssignment(t *testing.T) {
	start := allowanceStart
	model := planCard("pro", start.Add(time.Minute), 5)
	model.Unit = "app:tokens"
	model.ModelPrices = map[string]int64{"premium": 20}
	olderModel := planCard("pro", start, 1)
	olderModel.Unit = model.Unit
	olderModel.ModelPrices = map[string]int64{"premium": 2}
	request := planCard("pro", start.Add(2*time.Minute), 10)
	at := start.Add(3 * time.Minute)
	cards := []state.APIConsumerRateCard{request, model, olderModel}
	timeline := PlanCardTimeline(cards, []state.APIConsumerPlanAssignment{{PlanID: "pro", EffectiveFrom: at}})
	if len(timeline) != 2 || !timeline[0].EffectiveFrom.Equal(at) || !timeline[1].EffectiveFrom.Equal(at) {
		t.Fatalf("timeline = %+v, want both cards starting at assignment", timeline)
	}
	quote, err := QuoteConsumerUsage(timeline, []state.APIConsumerUsageBucket{usageAt(at, 1)}, ConsumerUsageInputs{
		Meters: map[string][]state.APIConsumerMeterUsageBucket{"tokens__premium": {{WindowStart: at, Units: 50}}},
	}, at, QuoteOptions{})
	if err != nil || !quote.Priced || quote.AmountMillicents != 1010 || len(quote.Buckets) != 2 {
		t.Fatalf("assigned plan quote = %+v, err = %v", quote, err)
	}
	if quote.Buckets[0].RateCardID != request.ID || quote.Buckets[1].RateCardID != model.ID || quote.Buckets[1].Model != "premium" {
		t.Fatalf("assigned plan buckets = %+v, want request and effective model cards", quote.Buckets)
	}
}

// adr: 953 — an earlier plan's rates end at the next assignment.
func TestPlanCardTimelineEndsModelRateWhenLeavingPlan(t *testing.T) {
	start := allowanceStart
	switchAt := start.Add(time.Minute)
	monthEnd := start.AddDate(0, 1, 0)
	model := planCard("pro", start.Add(-time.Hour), 5)
	model.Unit, model.ModelPrices = "app:tokens", map[string]int64{"premium": 20}
	model.BaseFeeMillicentsPerMonth = int64(monthEnd.Sub(start) / time.Minute)
	model.ProrateMonthlyFees = true
	request := planCard("pro", start.Add(-time.Minute), 10)
	defaultRequest := planCard("", start.Add(-time.Hour), 2)
	timeline := PlanCardTimeline([]state.APIConsumerRateCard{request, model, defaultRequest}, []state.APIConsumerPlanAssignment{
		{PlanID: "pro", EffectiveFrom: start}, {EffectiveFrom: switchAt},
	})
	quote, err := QuoteConsumerUsage(timeline, []state.APIConsumerUsageBucket{usageAt(start, 1), usageAt(switchAt, 1)}, ConsumerUsageInputs{
		Meters: map[string][]state.APIConsumerMeterUsageBucket{
			"tokens__premium": {{WindowStart: start, Units: 50}, {WindowStart: switchAt, Units: 50}},
			"tokens__preview": {{WindowStart: switchAt, Units: 17}},
		},
	}, start, QuoteOptions{})
	if err != nil || !quote.Priced || quote.UnpricedUnits != 0 || quote.AmountMillicents != 1012 || len(quote.Buckets) != 3 {
		t.Fatalf("quote across plan switch = %+v, err = %v", quote, err)
	}
	if got := quote.Buckets[2]; got.RateCardID != defaultRequest.ID || !got.WindowStart.Equal(switchAt) {
		t.Fatalf("switch minute = %+v, want default request pricing", got)
	}
	if MonthlyFeeCardEffectiveIn(timeline, switchAt, switchAt.Add(time.Minute)) {
		t.Fatal("expired model card imposes a monthly statement period on the default plan")
	}
	fees := APIConsumerStatementFees(timeline, start, monthEnd, start.Add(-time.Hour), QuoteOptions{}, nil, quote.Buckets)
	if len(fees) != 1 || fees[0].RateCardID != model.ID || fees[0].AmountMillicents != 1 {
		t.Fatalf("model fees = %+v, want one millicent for one active minute", fees)
	}
}

// adr: 949 — a closed price stream must not invalidate a finalized snapshot.
func TestAPIConsumerStatementDeltaPreservesExpiredPlanCoverage(t *testing.T) {
	start := allowanceStart
	switchAt := start.Add(time.Minute)
	lateAt := switchAt.Add(time.Minute)
	request := planCard("", start.Add(-time.Hour), 10)
	compute := planCard("", start.Add(-time.Hour), 20)
	compute.Unit = state.APIConsumerRateCardUnitComputeSecond
	basic := planCard("basic", start.Add(-time.Hour), 5)
	resolved := PlanCardTimeline([]state.APIConsumerRateCard{request, compute, basic}, []state.APIConsumerPlanAssignment{{PlanID: "basic", EffectiveFrom: switchAt}})
	legacy := append([]state.APIConsumerRateCard(nil), resolved...)
	for i := range legacy {
		legacy[i].EffectiveUntil = time.Time{}
	}
	usage := []state.APIConsumerUsageBucket{usageAt(start, 1), usageAt(switchAt, 1)}
	usage[0].ComputeMS, usage[1].ComputeMS = 2000, 2000
	quoted, err := QuoteConsumerUsage(legacy, usage, ConsumerUsageInputs{}, start, QuoteOptions{})
	if err != nil || !quoted.Priced || quoted.AmountMillicents != 95 {
		t.Fatalf("legacy quote = %+v, err = %v", quoted, err)
	}
	finalized := []state.APIConsumerUsageStatement{finalizedFrom(quoted)}

	// More compute in the inactive unit is not charged. The newly arrived
	// request is the only item in the adjustment.
	usage[1].ComputeMS += 1000
	usage = append(usage, usageAt(lateAt, 1))
	current, err := QuoteConsumerUsage(resolved, usage, ConsumerUsageInputs{}, start, QuoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	delta, err := APIConsumerStatementDelta(current, finalized)
	if err != nil || delta.AmountMillicents != 5 || len(delta.Buckets) != 1 || !delta.Buckets[0].WindowStart.Equal(lateAt) {
		t.Fatalf("late usage adjustment = %+v, err = %v", delta, err)
	}
	current, err = QuoteConsumerUsage(resolved, usage[1:], ConsumerUsageInputs{}, start, QuoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := APIConsumerStatementDelta(current, finalized); !errors.Is(err, ErrAPIConsumerUsageRegressed) {
		t.Fatalf("missing usage in an active stream: got %v, want ErrAPIConsumerUsageRegressed", err)
	}
}

// adr: 20261019100332 — a card's usage covers its prorated minimum once.
func TestAPIConsumerStatementFeesCombineRepeatedPlanAssignments(t *testing.T) {
	start := allowanceStart
	end := start.AddDate(0, 1, 0)
	leave := start.AddDate(0, 0, 10)
	returnAt := start.AddDate(0, 0, 20)
	pro := planCard("pro", start.Add(-time.Hour), 0)
	basic := planCard("basic", start.Add(-time.Hour), 0)
	model := planCard("pro", start.Add(-2*time.Hour), 5)
	model.Unit, model.ModelPrices = "app:tokens", map[string]int64{"premium": 20}
	model.ProrateMonthlyFees, model.BaseFeeMillicentsPerMonth, model.MinimumMillicentsPerMonth = true, 15000, 30000
	resolved := PlanCardTimeline([]state.APIConsumerRateCard{pro, basic, model}, []state.APIConsumerPlanAssignment{
		{PlanID: "pro", EffectiveFrom: start}, {PlanID: "basic", EffectiveFrom: leave}, {PlanID: "pro", EffectiveFrom: returnAt},
	})
	usage := []state.APIConsumerUsageBucket{usageAt(start, 1), usageAt(returnAt, 1)}
	quoted, err := QuoteConsumerUsage(resolved, usage, ConsumerUsageInputs{Meters: map[string][]state.APIConsumerMeterUsageBucket{
		"tokens__premium": {{WindowStart: start, Units: 250}, {WindowStart: returnAt, Units: 250}},
	}}, start, QuoteOptions{})
	if err != nil || quoted.AmountMillicents != 10000 {
		t.Fatalf("usage = %+v, err = %v", quoted, err)
	}
	created := start.Add(-time.Hour)
	fees := APIConsumerStatementFees(resolved, start, end, created, QuoteOptions{}, nil, quoted.Buckets)
	if len(fees) != 2 || fees[0] != (state.APIConsumerStatementFee{Kind: state.StatementFeeBase, RateCardID: model.ID, AmountMillicents: 10000}) ||
		fees[1] != (state.APIConsumerStatementFee{Kind: state.StatementFeeMinimum, RateCardID: model.ID, AmountMillicents: 10000}) {
		t.Fatalf("fees = %+v, want combined base fee and minimum top-up", fees)
	}
	finalized := finalizedFrom(quoted)
	finalized.Fees = fees
	adjustment := APIConsumerStatementFees(resolved, start, end, created, QuoteOptions{}, []state.APIConsumerUsageStatement{finalized},
		[]APIConsumerUsageChargeBucket{{RateCardID: model.ID, Unit: model.Unit, AmountMillicents: 1000}})
	if len(adjustment) != 1 || adjustment[0] != (state.APIConsumerStatementFee{Kind: state.StatementFeeMinimum, RateCardID: model.ID, AmountMillicents: -1000}) {
		t.Fatalf("late usage fees = %+v, want one reduction against the minimum", adjustment)
	}
}

func TestQuotePlatformTenantUsageRejectsPlanCards(t *testing.T) {
	if _, err := QuotePlatformTenantUsage([]state.APIConsumerRateCard{planCard("pro", allowanceStart, 1)}, nil,
		[]state.APIConsumerUsageBucket{usageAt(allowanceStart, 1)}); err == nil {
		t.Fatal("tenant statements must not price apps that use consumer plans")
	}
}
