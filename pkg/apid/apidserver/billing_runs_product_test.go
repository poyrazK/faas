package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261018212918 — a billing run with the product_customers source
// bills each Product Control customer's month: it finalizes and exports a
// resolved month, holds one with runs that have no outcome, skips a statement
// handed off by hand, leaves an exported one up to date when run again, and
// is refused while Product Control is disabled.
func TestBillingRunBillsProductControlCustomers(t *testing.T) {
	f := newBillingRunFixture(t)
	ctx := context.Background()
	store, acct := *f.e.Store(), *f.e.Acct()
	month := f.month.Format("2006-01")
	req := api.CreateBillingRunRequest{Month: month, Sources: []string{state.BillingRunSourceProductCustomers}, Export: state.BillingRunExportStripe}
	if res := f.e.Do(t, http.MethodPost, "/v1/account/billing-runs", req, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("product_customers while Product Control is disabled: %d %s", res.Code, res.Body)
	}
	(*f.e.S()).WithProductControlEnabled(true)

	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "billed-product", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := store.PutProductCatalog(ctx, acct.ID, app.ID, api.ProductCatalogSpec{Currency: "EUR",
		Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}},
		Plans:    map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}})
	if err != nil {
		t.Fatal(err)
	}
	customer := func(ref string, outcomes ...api.OperationState) state.PlatformTenant {
		t.Helper()
		tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, ref, ref, 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"}); err != nil {
			t.Fatal(err)
		}
		for _, outcome := range outcomes {
			store.SeedProductOperationForTest(state.Operation{AccountID: acct.ID, PlatformTenantID: tenant.ID, AppID: app.ID,
				OperationResponse: api.OperationResponse{ID: uuid.NewString(), State: outcome, UpdatedAt: f.month.Add(2 * time.Hour)},
				ProductControl: &api.ProductOperationReceipt{Revision: catalog.Revision, Feature: "export", Plan: "starter", Currency: "EUR",
					Period: month, UnitPriceMillicents: 25000, AdmittedAt: f.month.Add(time.Hour)}})
		}
		return tenant
	}
	alice := customer("cus_alice", api.OperationSucceeded, api.OperationSucceeded, api.OperationFailed)
	customer("cus_bob", api.OperationSucceeded, api.OperationAccepted)
	carol := customer("cus_carol", api.OperationSucceeded)
	customer("cus_dave") // assigned, no receipts: not billed
	carolScope := state.ProductBillingScope{AccountID: acct.ID, AppID: app.ID, TenantID: carol.ID, Period: month}
	review, err := store.ReviewProductBilling(ctx, carolScope)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := store.FinalizeProductBilling(ctx, carolScope, review.ReviewHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.HandoffProductBilling(ctx, carolScope, statement.ID, "INV-carol"); err != nil {
		t.Fatal(err)
	}

	run := f.start(t, req)
	done := f.drain(t, run.ID)
	items := f.items(t, run.ID)
	if len(items) != 3 || done.Counts.Exported != 1 || done.Counts.Held != 1 || done.Counts.Skipped != 1 {
		t.Fatalf("run = %+v, items = %+v", done.Counts, items)
	}
	paid := items["cus_alice"]
	if paid.SourceKind != state.StatementSourceProductControl || paid.Outcome != state.BillingRunItemExported || paid.AppID != app.ID ||
		paid.PlatformTenantID != alice.ID || paid.StatementID == "" || paid.InvoiceID == "" || paid.AmountMillicents == nil || *paid.AmountMillicents != 50000 {
		t.Fatalf("alice = %+v", paid)
	}
	if held := items["cus_bob"]; held.Outcome != state.BillingRunItemHeld || !strings.Contains(held.Reason, "product_billing_unresolved") {
		t.Fatalf("bob = %+v", held)
	}
	if skipped := items["cus_carol"]; skipped.Outcome != state.BillingRunItemSkipped || skipped.StatementID != statement.ID || !strings.Contains(skipped.Reason, "by hand") {
		t.Fatalf("carol = %+v", skipped)
	}
	if export, err := store.GetStatementInvoiceExport(ctx, acct.ID, state.StatementSourceProductControl, paid.StatementID); err != nil ||
		export.Status != state.StatementInvoiceExportCompleted || export.Customer != "cus_alice" || export.ProviderInvoiceID != paid.InvoiceID {
		t.Fatalf("alice's export = %+v, %v", export, err)
	}
	if handoff, err := store.GetProductBillingHandoff(ctx, state.ProductBillingScope{AccountID: acct.ID, AppID: app.ID, TenantID: alice.ID, Period: month},
		paid.StatementID); err != nil || handoff.ExternalInvoiceID != paid.InvoiceID {
		t.Fatalf("alice's handoff = %+v, %v", handoff, err)
	}

	again := f.drain(t, f.start(t, req).ID)
	if items := f.items(t, again.ID); items["cus_alice"].Outcome != state.BillingRunItemUpToDate || items["cus_bob"].Outcome != state.BillingRunItemHeld ||
		items["cus_carol"].Outcome != state.BillingRunItemSkipped {
		t.Fatalf("second run = %+v", items)
	}
	if f.stripe.invoices != 1 {
		t.Fatalf("stripe invoices = %d, want one for alice", f.stripe.invoices)
	}
}
