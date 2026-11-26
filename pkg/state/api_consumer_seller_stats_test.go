package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkSellerStats runs the ADR-20261126082046 store contract: usage sums by
// consumer and UTC month, route totals by prefix with distinct consumers,
// draft and finalized statements by period start, and every plan assignment
// of the app.
func checkSellerStats(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	stats := st.(state.SellerStatsStore)
	usage := st.(state.ConsumerUsageStore)
	statements := st.(state.APIConsumerUsageStatementStore)
	plans := st.(state.APIConsumerPlanStore)
	a, err := st.CreateAPIConsumer(ctx, accountID, appID, "stats-a", "A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateAPIConsumer(ctx, accountID, appID, "stats-b", "B")
	if err != nil {
		t.Fatal(err)
	}
	thisMonth := state.UTCMonthStart(time.Now())
	lastMonth := thisMonth.AddDate(0, -1, 0)
	record := func(consumer string, at time.Time, route string, units int64) {
		t.Helper()
		if _, err := usage.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: accountID,
			AppID: appID, ConsumerKey: consumer, WindowStart: at, RequestCount: units, BillableUnits: units, BillingRoute: route}); err != nil {
			t.Fatal(err)
		}
	}
	record(a.ID, lastMonth.Add(time.Hour), "MCP /tools/search", 2)
	record(a.ID, thisMonth.Add(time.Minute), "MCP /tools/search", 3)
	record(a.ID, thisMonth.Add(2*time.Minute), "MCP /tools/fetch", 1)
	record(b.ID, thisMonth.Add(time.Minute), "MCP /tools/search", 5)
	record(b.ID, thisMonth.Add(3*time.Minute), "POST /other", 7)

	sums, err := stats.SumAPIConsumerUsageByMonth(ctx, accountID, appID, lastMonth, thisMonth.AddDate(0, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]state.APIConsumerMonthUsage{}
	for _, s := range sums {
		got[s.ConsumerKey+"@"+s.Month.Format("2006-01")] = s
	}
	if len(sums) != 3 || got[a.ID+"@"+lastMonth.Format("2006-01")].BillableUnits != 2 ||
		got[a.ID+"@"+thisMonth.Format("2006-01")].BillableUnits != 4 || got[b.ID+"@"+thisMonth.Format("2006-01")].Requests != 12 ||
		got[b.ID+"@"+thisMonth.Format("2006-01")].BillableUnits != 12 {
		t.Fatalf("month sums = %+v", sums)
	}

	routes, err := stats.SumAPIConsumerRouteUsage(ctx, accountID, appID, "MCP /tools/", thisMonth, thisMonth.AddDate(0, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[0] != (state.APIConsumerRouteTotal{Route: "MCP /tools/search", BillableUnits: 8, Consumers: 2}) ||
		routes[1] != (state.APIConsumerRouteTotal{Route: "MCP /tools/fetch", BillableUnits: 1, Consumers: 1}) {
		t.Fatalf("route totals = %+v", routes)
	}

	draft, _, err := statements.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: a.ID, Revision: 1, PeriodStart: lastMonth, PeriodEnd: thisMonth,
		Currency: "USD", BillableUnits: 2, AmountMillicents: 500, Priced: true, AsOf: thisMonth,
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: lastMonth, BillableUnits: 2, RateCardID: uuid.NewString(),
			Currency: "USD", PriceMillicentsPerUnit: 250, AmountMillicents: 500}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := statements.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: b.ID, Revision: 1, PeriodStart: lastMonth, PeriodEnd: thisMonth,
		Currency: "USD", BillableUnits: 3, AmountMillicents: 900, Priced: true, AsOf: thisMonth,
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: lastMonth, BillableUnits: 3, RateCardID: uuid.NewString(),
			Currency: "USD", PriceMillicentsPerUnit: 300, AmountMillicents: 900}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := statements.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, a.ID, draft.ID); err != nil {
		t.Fatal(err)
	}
	listed, err := stats.ListAPIConsumerStatementsForApp(ctx, accountID, appID, lastMonth, thisMonth)
	byConsumer := map[string]state.APIConsumerUsageStatement{}
	for _, s := range listed {
		byConsumer[s.ConsumerID] = s
	}
	if err != nil || len(listed) != 2 || byConsumer[a.ID].Status != state.APIConsumerUsageStatementFinalized ||
		byConsumer[a.ID].AmountMillicents != 500 || byConsumer[b.ID].Status != state.APIConsumerUsageStatementDraft {
		t.Fatalf("statements = %+v, %v; want a's finalized and b's draft", listed, err)
	}

	plan, err := plans.CreateAPIConsumerPlan(ctx, state.APIConsumerPlan{AccountID: accountID, AppID: appID, Name: "pro", MaxUnitsPerMonth: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{a.ID, b.ID} {
		if _, err := plans.AssignAPIConsumerPlan(ctx, state.APIConsumerPlanAssignment{AccountID: accountID, AppID: appID, ConsumerID: c,
			PlanID: plan.ID, EffectiveFrom: thisMonth}); err != nil {
			t.Fatal(err)
		}
	}
	assignments, err := stats.ListAPIConsumerPlanAssignmentsForApp(ctx, accountID, appID)
	if err != nil || len(assignments) != 2 || assignments[0].PlanID != plan.ID {
		t.Fatalf("assignments = %+v, %v", assignments, err)
	}
}

// adr: 20261126082046
func TestMemStoreSellerStats(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkSellerStats(t, ctx, store, accountID, appID)
}

// adr: 20261126082046
func TestPgStoreSellerStats(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkSellerStats(t, ctx, store, accountID, appID)
}
