package state

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// productPlanSeed appends a backdated assignment to a customer's plan
// history.
type productPlanSeed func(account, app, tenant, revision, plan string, first bool, at time.Time)

func TestProductPlanFeesMemory(t *testing.T) {
	s := NewMemStore()
	productPlanFeesContract(t, s, s.SeedProductPlanHistoryForTest)
}

// adr: 20261021154511 — a month's statement charges, once, the highest
// monthly fee among the plans the customer held in it, prorated by the minute
// in the month of their first assignment; a month with a fee is billed
// without runs, and billing runs discover such customers.
func productPlanFeesContract(t *testing.T, s productBillingRunTestStore, seed productPlanSeed) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, "product-fees-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: account.ID, Slug: "product-fees"})
	if err != nil {
		t.Fatal(err)
	}
	grant := map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 1000}}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}},
		Plans: map[string]api.ProductPlan{"free": {Label: "Free", Features: grant}, "starter": {Label: "Starter", Features: grant, MonthlyFeeMillicents: 1000000},
			"pro": {Label: "Pro", Features: grant, MonthlyFeeMillicents: 2900000}}}
	catalog, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	tenant := func(name string) string {
		value, _, err := s.CreatePlatformTenant(ctx, account.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		return value.ID
	}
	assign := func(tenant, plan string) {
		if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant, Plan: plan}); err != nil {
			t.Fatal(err)
		}
	}
	review := func(tenant, period string) api.ProductBillingReview {
		t.Helper()
		value, err := s.ReviewProductBilling(ctx, ProductBillingScope{AccountID: account.ID, AppID: app.ID, TenantID: tenant, Period: period})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	now := time.Now().UTC()
	thisMonth := now.Format("2006-01")

	// Assignments made now: the first is prorated, and the month owes the
	// highest fee held.
	live := tenant("live")
	assign(live, "starter")
	first := review(live, thisMonth)
	if len(first.Fees) != 1 || first.Fees[0].Plan != "starter" || first.Fees[0].Revision != catalog.Revision || first.Fees[0].MonthlyFeeMillicents != 1000000 ||
		first.Fees[0].AmountMillicents <= 0 || first.Fees[0].AmountMillicents > 1000000 || first.AmountMillicents != first.Fees[0].AmountMillicents {
		t.Fatalf("first month review = %+v", first)
	}
	assign(live, "pro")
	assign(live, "free")
	assign(live, "free")
	held := review(live, thisMonth)
	if len(held.Fees) != 1 || held.Fees[0].Plan != "pro" || held.Fees[0].MonthlyFeeMillicents != 2900000 || held.ReviewHash == first.ReviewHash ||
		(held.Fees[0].ProratedFrom == nil) != (first.Fees[0].ProratedFrom == nil) {
		t.Fatalf("review after upgrade and downgrade = %+v", held)
	}

	// A closed month: held starter from before it and pro from the 20th,
	// with no runs, owes pro's whole fee and is finalized for it.
	lastStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	lastEnd := lastStart.AddDate(0, 1, 0)
	lastMonth := lastStart.Format("2006-01")
	upgraded := tenant("upgraded")
	assign(upgraded, "free")
	seed(account.ID, app.ID, upgraded, catalog.Revision, "starter", true, lastStart.AddDate(0, 0, -10))
	seed(account.ID, app.ID, upgraded, catalog.Revision, "pro", false, lastStart.AddDate(0, 0, 19))
	closed := review(upgraded, lastMonth)
	if !closed.Closed || closed.Starts != 0 || len(closed.Fees) != 1 || closed.Fees[0].Plan != "pro" || closed.Fees[0].AmountMillicents != 2900000 ||
		closed.Fees[0].ProratedFrom != nil || closed.AmountMillicents != 2900000 {
		t.Fatalf("closed month review = %+v", closed)
	}
	statement, err := s.FinalizeProductBilling(ctx, ProductBillingScope{AccountID: account.ID, AppID: app.ID, TenantID: upgraded, Period: lastMonth}, closed.ReviewHash)
	if err != nil || statement.AmountMillicents != 2900000 || statement.Snapshot == nil || len(statement.Snapshot.Fees) != 1 {
		t.Fatalf("finalized fee-only month = %+v %v", statement, err)
	}
	if before := review(upgraded, lastStart.AddDate(0, -2, 0).Format("2006-01")); len(before.Fees) != 0 || before.AmountMillicents != 0 {
		t.Fatalf("a month before any assignment owes %+v", before)
	}

	// The first assignment on the 21st of the closed month prorates it.
	joined := tenant("joined")
	assign(joined, "free")
	joinedAt := lastStart.AddDate(0, 0, 20).Add(90 * time.Second)
	seed(account.ID, app.ID, joined, catalog.Revision, "starter", true, joinedAt)
	total, share := int64(lastEnd.Sub(lastStart)/time.Minute), int64(lastEnd.Sub(joinedAt.Truncate(time.Minute))/time.Minute)
	prorated := review(joined, lastMonth)
	if len(prorated.Fees) != 1 || prorated.Fees[0].AmountMillicents != (1000000*share+total/2)/total || prorated.Fees[0].ProratedFrom == nil ||
		!prorated.Fees[0].ProratedFrom.Equal(joinedAt.Truncate(time.Minute)) {
		t.Fatalf("prorated month = %+v", prorated)
	}

	// A billing run for the closed month discovers both fee customers
	// without receipts, and not the customer assigned only this month.
	run, err := s.CreateBillingRun(ctx, BillingRun{AccountID: account.ID, PeriodStart: lastStart, PeriodEnd: lastEnd, Trigger: BillingRunTriggerAPI,
		BillingRunOptions: BillingRunOptions{Sources: []string{BillingRunSourceProductCustomers}, Finalize: true, Export: BillingRunExportNone}})
	if err != nil {
		t.Fatal(err)
	}
	owner := uuid.NewString()
	if claimed, err := s.ClaimBillingRun(ctx, owner, now, time.Minute); err != nil || claimed.ID != run.ID {
		t.Fatalf("claim = %+v %v", claimed, err)
	}
	if err := s.DiscoverBillingRunItems(ctx, run.ID, owner, "tenant", now); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListBillingRunItems(ctx, account.ID, run.ID, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, item := range items {
		found[item.SourceID] = item.SourceKind == StatementSourceProductControl && item.AppID == app.ID
	}
	if len(items) != 2 || !found[upgraded] || !found[joined] {
		t.Fatalf("discovered items = %+v", items)
	}
}
