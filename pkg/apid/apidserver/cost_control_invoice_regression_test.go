package apidserver_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCostControlOrgCostReportRespectsInvoiceRole(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	owner, err := store.CreateAccount(ctx, "audit-invoice-owner@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "audit-invoice-org", Name: "Audit", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	viewer, err := store.CreateAccount(ctx, "audit-invoice-viewer@example.test", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, viewer.ID, state.OrgRoleViewer, nil); err != nil {
		t.Fatal(err)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOrgAPIKey(ctx, org.ID, viewer.ID, hash, "audit-usage", []string{api.ScopeUsageRead}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: org.ID, Provider: "polar", CustomerID: "audit-org-customer", SubscriptionID: "audit-subscription"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if err := store.UpsertInvoice(ctx, state.Invoice{AccountID: org.ID, Provider: "polar", ProviderInvoiceID: "audit-private-provider-invoice", Number: "AUDIT-PRIVATE-001", Status: "paid", PeriodStart: month, PeriodEnd: now, SubtotalCents: 10000, TaxCents: 2000, TotalCents: 12000, AmountPaidCents: 12000, Currency: "usd", Plan: api.PlanPro}); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", nil)
	handler := srv.Handler()
	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+plain)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	denied := request("/v1/orgs/" + org.Slug + "/invoices")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("fixture viewer invoice endpoint=%d %s; want 403", denied.Code, denied.Body)
	}
	got := request("/v1/orgs/" + org.Slug + "/billing/costs")
	if got.Code != http.StatusOK {
		t.Fatalf("cost endpoint=%d %s", got.Code, got.Body)
	}
	var report api.FinancialCostsResponse
	if err := json.Unmarshal(got.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Invoices) > 0 {
		t.Errorf("viewer denied /invoices (403) receives %d full invoice record(s) through /billing/costs (200): number=%s provider_invoice_id=%s total=%d tax=%d paid=%d", len(report.Invoices), report.Invoices[0].Number, report.Invoices[0].ProviderInvoiceID, report.Invoices[0].TotalCents, report.Invoices[0].TaxCents, report.Invoices[0].AmountPaidCents)
	}
}
