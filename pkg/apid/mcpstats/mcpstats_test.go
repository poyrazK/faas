package mcpstats_test

// adr: 20261126082046 — seller stats: funnel by source, billed and estimated
// revenue, units and revenue per tool, and customers near their limit.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcpstats"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRead(t *testing.T) {
	ctx := context.Background()
	st := state.NewMemStore()
	acct, err := st.CreateAccount(ctx, "seller@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	appRow, err := st.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "tools", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	app := mcpstats.App{Slug: "tools", ID: appRow.ID, AccountID: acct.ID}
	now := time.Now().UTC()
	thisMonth := state.UTCMonthStart(now)
	lastMonth := thisMonth.AddDate(0, -1, 0)

	owner, _ := st.CreateAPIConsumer(ctx, acct.ID, app.ID, "crm-42", "Owner made")
	oauth, _ := st.CreateAPIConsumer(ctx, acct.ID, app.ID, "oauth:x", "Ada")
	signup, _ := st.CreateAPIConsumer(ctx, acct.ID, app.ID, "signup:y", "Bob")
	plan, err := st.CreateAPIConsumerPlan(ctx, state.APIConsumerPlan{AccountID: acct.ID, AppID: app.ID, Name: "free", MaxUnitsPerMonth: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{owner.ID, oauth.ID} {
		if _, err := st.AssignAPIConsumerPlan(ctx, state.APIConsumerPlanAssignment{AccountID: acct.ID, AppID: app.ID, ConsumerID: c,
			PlanID: plan.ID, EffectiveFrom: lastMonth}); err != nil {
			t.Fatal(err)
		}
	}
	record := func(consumer string, at time.Time, tool string, units int64) {
		t.Helper()
		if _, err := st.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID,
			ConsumerKey: consumer, WindowStart: at, RequestCount: units, BillableUnits: units, BillingRoute: api.MCPToolBillingLabel(tool)}); err != nil {
			t.Fatal(err)
		}
	}
	record(owner.ID, lastMonth.Add(time.Hour), "search", 4)
	record(oauth.ID, thisMonth, "search", 9) // 90% of the free plan
	record(owner.ID, thisMonth, "fetch", 2)
	// Anonymous traffic adds units but is no customer.
	if _, err := st.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID,
		ConsumerKey: state.AnonymousConsumerKey, WindowStart: thisMonth, RequestCount: 5, BillableUnits: 5}); err != nil {
		t.Fatal(err)
	}
	statement, _, err := st.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{AccountID: acct.ID, AppID: app.ID,
		ConsumerID: owner.ID, Revision: 1, PeriodStart: lastMonth, PeriodEnd: thisMonth, Currency: "USD", BillableUnits: 4,
		AmountMillicents: 4000, Priced: true, AsOf: thisMonth,
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: lastMonth, BillableUnits: 4, RateCardID: uuid.NewString(),
			Currency: "USD", PriceMillicentsPerUnit: 1000, AmountMillicents: 4000}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.FinalizeAPIConsumerUsageStatement(ctx, acct.ID, app.ID, owner.ID, statement.ID); err != nil {
		t.Fatal(err)
	}
	// The month-end run has not finalized oauth's last month yet: its
	// draft counts as an estimate.
	if _, _, err := st.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{AccountID: acct.ID, AppID: app.ID,
		ConsumerID: oauth.ID, Revision: 1, PeriodStart: lastMonth, PeriodEnd: thisMonth, Currency: "USD", BillableUnits: 1,
		AmountMillicents: 700, Priced: true, AsOf: thisMonth,
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: lastMonth, BillableUnits: 1, RateCardID: uuid.NewString(),
			Currency: "USD", PriceMillicentsPerUnit: 700, AmountMillicents: 700}}}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	quoted := map[string]bool{}
	quote := func(_ context.Context, _, _, consumerID string, since, _ time.Time) (billing.APIConsumerUsageQuote, error) {
		mu.Lock()
		quoted[consumerID] = true
		mu.Unlock()
		if !since.Equal(thisMonth) {
			t.Errorf("quote since %v, want this month", since)
		}
		switch consumerID {
		case oauth.ID:
			return billing.APIConsumerUsageQuote{Currency: "USD", AmountMillicents: 900,
				RouteBreakdown: []api.APIConsumerQuoteBreakdown{{Route: api.MCPToolBillingLabel("search"), AmountMillicents: 900}}}, nil
		case signup.ID:
			return billing.APIConsumerUsageQuote{}, errors.New("price history unreadable")
		}
		return billing.APIConsumerUsageQuote{Currency: "USD"}, nil
	}

	got, err := mcpstats.Read(ctx, st, quote, app, 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Months) != 2 || got.Months[0].Month != lastMonth.Format("2006-01") || !got.Months[1].Current {
		t.Fatalf("months = %+v", got.Months)
	}
	cur, prev := got.Months[1], got.Months[0]
	if cur.SignedUp != 3 || cur.SignedUpBySource[api.MCPSellerSourceOAuth] != 1 || cur.SignedUpBySource[api.MCPSellerSourceSignup] != 1 ||
		cur.SignedUpBySource[api.MCPSellerSourceOwner] != 1 {
		t.Fatalf("sign-ups = %d %v", cur.SignedUp, cur.SignedUpBySource)
	}
	if cur.Active != 2 || cur.Units != 16 || cur.Paying != 1 || len(cur.Revenue) != 1 || cur.Revenue[0].EstimatedMillicents != 900 {
		t.Fatalf("current month = %+v", cur)
	}
	if prev.Active != 1 || prev.Units != 4 || prev.Paying != 2 || len(prev.Revenue) != 1 || prev.Revenue[0].BilledMillicents != 4000 ||
		prev.Revenue[0].EstimatedMillicents != 700 {
		t.Fatalf("last month = %+v", prev)
	}
	if got.EstimateComplete || !quoted[signup.ID] {
		t.Fatal("an unpriceable customer left the estimate marked complete")
	}
	if len(got.Tools) != 2 || got.Tools[0].Tool != "search" || got.Tools[0].Units != 13 || got.Tools[0].Customers != 2 ||
		len(got.Tools[0].Revenue) != 1 || got.Tools[0].Revenue[0].EstimatedMillicents != 900 || got.Tools[1].Tool != "fetch" {
		t.Fatalf("tools = %+v", got.Tools)
	}
	if len(got.NearLimit) != 1 || got.NearLimit[0].ConsumerID != oauth.ID || got.NearLimit[0].Percent != 90 || got.NearLimit[0].Plan != "free" {
		t.Fatalf("near limit = %+v", got.NearLimit)
	}
	if got.Customers.Open != 3 || got.Customers.Closed != 0 {
		t.Fatalf("customers = %+v", got.Customers)
	}

	for _, months := range []int{0, api.MCPSellerStatsMaxMonths + 1} {
		if _, err := mcpstats.Read(ctx, st, quote, app, months, now); !errors.Is(err, mcpstats.ErrMonths) {
			t.Errorf("months %d: %v", months, err)
		}
	}
}
