package state

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type productBillingRunTestStore interface {
	productBillingTestStore
	BillingRunStore
}

// adr: 20261018212918
func TestProductBillingRunItemsMemory(t *testing.T) {
	s := NewMemStore()
	insert := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.insertProductOperationLocked(op) }
	settle := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.settleProductOperationLocked(op) }
	productBillingRunContract(t, s, seedProductBilling(t, s, insert, settle), insert, settle)
}

func TestBillingRunDiscoveredNeedsProductCustomers(t *testing.T) {
	at := time.Now()
	for _, tc := range []struct {
		run  BillingRun
		want bool
	}{
		{BillingRun{BillingRunOptions: BillingRunOptions{Sources: []string{BillingRunSourceAppConsumers}}, DiscoveredAt: &at}, true},
		// A previous release discovers usage customers only.
		{BillingRun{BillingRunOptions: BillingRunOptions{Sources: []string{BillingRunSourceProductCustomers}}, DiscoveredAt: &at}, false},
		{BillingRun{BillingRunOptions: BillingRunOptions{Sources: []string{BillingRunSourceProductCustomers}}, DiscoveredAt: &at, ProductDiscoveredAt: &at}, true},
		{BillingRun{BillingRunOptions: BillingRunOptions{Sources: []string{BillingRunSourceProductCustomers}}, ProductDiscoveredAt: &at}, false},
	} {
		if got := tc.run.Discovered(); got != tc.want {
			t.Errorf("%+v discovered = %v", tc.run, got)
		}
	}
	for _, sources := range [][]string{
		{BillingRunSourceProductCustomers},
		{BillingRunSourceAppConsumers, BillingRunSourcePlatformTenants, BillingRunSourceProductCustomers},
	} {
		if err := ValidateBillingRunOptions(BillingRunOptions{Sources: sources, Finalize: true, Export: BillingRunExportNone}); err != nil {
			t.Errorf("%v: %v", sources, err)
		}
	}
	for _, sources := range [][]string{
		{BillingRunSourceProductCustomers, BillingRunSourceProductCustomers},
		{"product_control"},
		{BillingRunSourceAppConsumers, BillingRunSourcePlatformTenants, BillingRunSourceProductCustomers, "extra"},
	} {
		if err := ValidateBillingRunOptions(BillingRunOptions{Sources: sources, Finalize: true, Export: BillingRunExportNone}); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("%v accepted: %v", sources, err)
		}
	}
}

// productBillingRunContract checks Product Control items of a billing run
// (ADR-20261018212918): one item per assigned customer and app with receipts
// in the month, keyed by app so a tenant can be a customer of two apps,
// paged after every other kind, counted, leased and completing the run.
func productBillingRunContract(t *testing.T, s productBillingRunTestStore, f productBillingFixture, insert, settle func(Operation)) {
	t.Helper()
	ctx := t.Context()
	account := f.scope.AccountID
	second, err := s.CreateApp(ctx, App{AccountID: account, Slug: "second-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"report": {Label: "Report", Operation: &api.ProductOperationBinding{Name: "report", Scope: api.DefaultEnvScope}}},
		Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"report": {MonthlyStarts: 10, UnitPriceMillicents: 1000}}}}}
	catalog, err := s.PutProductCatalog(ctx, account, second.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProductCustomer(ctx, account, second.ID, api.ProductCustomer{TenantID: f.scope.TenantID, Plan: "starter"}); err != nil {
		t.Fatal(err)
	}
	start, err := time.Parse("2006-01", f.scope.Period)
	if err != nil {
		t.Fatal(err)
	}
	op := Operation{AccountID: account, PlatformTenantID: f.scope.TenantID, AppID: second.ID, OperationResponse: api.OperationResponse{ID: uuid.NewString(),
		State: api.OperationSucceeded, UpdatedAt: start.Add(time.Hour)}, ProductControl: &api.ProductOperationReceipt{Revision: catalog.Revision, Feature: "report",
		Plan: "starter", Currency: "EUR", Period: f.scope.Period, UnitPriceMillicents: 1000, AdmittedAt: start}}
	insert(op)
	settle(op)

	usageOnly, err := s.CreateBillingRun(ctx, BillingRun{AccountID: account, PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), Trigger: BillingRunTriggerAPI,
		BillingRunOptions: BillingRunOptions{Sources: []string{BillingRunSourceAppConsumers}, Finalize: true, Export: BillingRunExportNone}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	owner := uuid.NewString()
	claim := func(want string) {
		t.Helper()
		run, err := s.ClaimBillingRun(ctx, owner, now, time.Minute)
		if err != nil || run.ID != want {
			t.Fatalf("claim = %+v, %v", run, err)
		}
	}
	claim(usageOnly.ID)
	if err := s.DiscoverBillingRunItems(ctx, usageOnly.ID, owner, "tenant", now); err != nil {
		t.Fatal(err)
	}
	if items, err := s.ListBillingRunItems(ctx, account, usageOnly.ID, "", "", 10); err != nil || len(items) != 0 {
		t.Fatalf("a run without product_customers billed Product Control customers: %+v, %v", items, err)
	}
	if done, err := s.ReleaseBillingRun(ctx, usageOnly.ID, owner, now); err != nil || done.Status != BillingRunCompleted {
		t.Fatalf("usage-only run = %+v, %v", done, err)
	}

	run, err := s.CreateBillingRun(ctx, BillingRun{AccountID: account, PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), Trigger: BillingRunTriggerAPI,
		BillingRunOptions: BillingRunOptions{Sources: []string{BillingRunSourceProductCustomers}, Finalize: true, Export: BillingRunExportNone}})
	if err != nil || run.Discovered() {
		t.Fatalf("run = %+v, %v", run, err)
	}
	claim(run.ID)
	for range 2 {
		if err := s.DiscoverBillingRunItems(ctx, run.ID, owner, "tenant", now); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.GetBillingRun(ctx, account, run.ID); err != nil || !got.Discovered() || got.Counts[BillingRunItemPending] != 4 {
		t.Fatalf("discovered run = %+v, %v", got, err)
	}
	want := map[string]bool{f.scope.AppID + "/" + f.scope.TenantID: true, f.scope.AppID + "/" + f.other.TenantID: true,
		f.scope.AppID + "/" + f.third.TenantID: true, second.ID + "/" + f.scope.TenantID: true}
	var items []BillingRunItem
	for after := ""; ; {
		page, err := s.ListBillingRunItems(ctx, account, run.ID, "", after, 3)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, page...)
		if len(page) < 3 {
			break
		}
		after = page[len(page)-1].Key()
	}
	if len(items) != 4 {
		t.Fatalf("items = %+v", items)
	}
	for i, item := range items {
		if item.SourceKind != StatementSourceProductControl || !want[item.AppID+"/"+item.SourceID] || item.Outcome != BillingRunItemPending || item.ExternalRef == "" ||
			(i > 0 && items[i-1].Key() >= item.Key()) {
			t.Fatalf("item %d = %+v", i, item)
		}
		delete(want, item.AppID+"/"+item.SourceID)
	}
	if _, err := s.ListBillingRunItems(ctx, uuid.NewString(), run.ID, "", "", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign account listed items: %v", err)
	}

	due, err := s.ListDueBillingRunItems(ctx, run.ID, owner, now, 3)
	if err != nil || len(due) != 3 {
		t.Fatalf("due = %+v, %v", due, err)
	}
	if done, err := s.ReleaseBillingRun(ctx, run.ID, owner, now); err != nil || done.Status == BillingRunCompleted {
		t.Fatalf("run with pending items completed: %+v, %v", done, err)
	}
	claim(run.ID)
	stale := items[0]
	stale.Outcome = BillingRunItemHeld
	if err := s.UpdateBillingRunItem(ctx, uuid.NewString(), stale); !errors.Is(err, ErrBillingRunLeaseLost) {
		t.Fatalf("update without the lease: %v", err)
	}
	for i, item := range items {
		item.Outcome, item.Attempts = BillingRunItemFinalized, 1
		item.StatementID, item.Currency, item.AmountMillicents = uuid.NewString(), "EUR", int64(1000*(i+1))
		if i == 0 {
			item.Outcome, item.StatementID, item.Currency, item.AmountMillicents = BillingRunItemHeld, "", "", 0
			item.Reason = "product_billing_unresolved: 2 runs need an outcome"
		}
		if err := s.UpdateBillingRunItem(ctx, owner, item); err != nil {
			t.Fatal(err)
		}
	}
	held, err := s.ListBillingRunItems(ctx, account, run.ID, BillingRunItemHeld, "", 10)
	if err != nil || len(held) != 1 || held[0].Reason != "product_billing_unresolved: 2 runs need an outcome" || held[0].StatementID != "" {
		t.Fatalf("held = %+v, %v", held, err)
	}
	done, err := s.ReleaseBillingRun(ctx, run.ID, owner, now)
	if err != nil || done.Status != BillingRunCompleted || done.Counts[BillingRunItemFinalized] != 3 || done.Counts[BillingRunItemHeld] != 1 {
		t.Fatalf("released run = %+v, %v", done, err)
	}
	finalized, err := s.ListBillingRunItems(ctx, account, run.ID, BillingRunItemFinalized, "", 10)
	if err != nil || len(finalized) != 3 || finalized[0].StatementID == "" || finalized[0].Currency != "EUR" || finalized[0].AmountMillicents == 0 {
		t.Fatalf("finalized = %+v, %v", finalized, err)
	}
}
