package product

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

type overdueTestHost struct {
	customerTestHost
	mu     sync.Mutex
	events []string
}

func (h *overdueTestHost) EmitAudit(_ context.Context, kind string, _ *string, data map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	event := kind
	if tenant, ok := data["tenant_id"].(string); ok {
		event += " " + tenant
	}
	h.events = append(h.events, event)
}

func (h *overdueTestHost) take() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	events := h.events
	h.events = nil
	return events
}

// adr: 20261018212918 — owners set and clear the overdue policy, every
// change and hold is audited, the worker only runs while Product Control is
// enabled, and the console lists held customers with the provider's page.
func TestProductOverduePolicyWorkerAndConsole(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "overdue-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "overdue-console"})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	doc := api.ProductBillingDocument{ID: uuid.NewString(), Kind: "statement", TenantID: tenant.ID, Period: "2026-08", Currency: "EUR", AmountMillicents: 25000}
	store.SeedProductBillingDocumentForTest(account.ID, app.ID, doc)
	export, _, err := store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: account.ID, SourceKind: state.StatementSourceProductControl,
		StatementID: doc.ID, Provider: state.BillingProviderStripe, Customer: "cus_1", Currency: "EUR", AmountMinor: 25})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatementInvoiceExportInvoice(ctx, account.ID, export.ID, "in_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteStatementInvoiceExport(ctx, account.ID, export.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	due := now.AddDate(0, 0, -20)
	if _, err := store.RecordStatementInvoicePayment(ctx, export.ID, state.StatementInvoicePayment{Status: state.InvoicePaymentOpen, AmountRemainingMinor: 25,
		DueAt: &due, HostedInvoiceURL: "https://invoice.stripe.com/i/in_1"}, now, nil); err != nil {
		t.Fatal(err)
	}
	host := &overdueTestHost{customerTestHost: customerTestHost{store: store}}
	s := &Service{Host: host, Now: func() time.Time { return now }}
	call := func(handler func(http.ResponseWriter, *http.Request, state.Account), method, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/", strings.NewReader(body))
		r.SetPathValue("slug", app.Slug)
		w := httptest.NewRecorder()
		handler(w, r, account)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, body, w.Code, status, w.Body)
		}
		return w
	}

	call(s.GetOverduePolicy, "GET", "", http.StatusNotFound)
	call(s.DeleteOverduePolicy, "DELETE", "", http.StatusNotFound)
	for _, body := range []string{`{}`, `{"block_overdue_after_days":366}`, `{"block_overdue_after_days":-1}`} {
		call(s.PutOverduePolicy, "PUT", body, http.StatusUnprocessableEntity)
	}
	call(s.PutOverduePolicy, "PUT", `{"block_overdue_after_days":14}`, http.StatusOK)
	var policy api.ProductOverduePolicy
	if err := json.Unmarshal(call(s.GetOverduePolicy, "GET", "", http.StatusOK).Body.Bytes(), &policy); err != nil || policy.BlockOverdueAfterDays != 14 || policy.AppID != app.ID {
		t.Fatalf("policy = %+v, %v", policy, err)
	}
	if events := host.take(); !slices.Equal(events, []string{"product.billing.overdue_policy_updated"}) {
		t.Fatalf("policy audit = %v", events)
	}

	if err := s.SyncOverdueOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if holds, _ := store.ListProductOverdueHolds(ctx, account.ID, app.ID); len(holds) != 0 || len(host.take()) != 0 {
		t.Fatalf("disabled Product Control placed %+v", holds)
	}
	s.Enabled = true
	if err := s.SyncOverdueOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if events := host.take(); !slices.Equal(events, []string{"product.customer.overdue_blocked " + tenant.ID}) {
		t.Fatalf("hold audit = %v", events)
	}
	var holds api.ProductOverdueHolds
	if err := json.Unmarshal(call(s.OverdueHolds, "GET", "", http.StatusOK).Body.Bytes(), &holds); err != nil || len(holds.Holds) != 1 ||
		holds.Holds[0].TenantID != tenant.ID || holds.Holds[0].DocumentID != doc.ID || holds.Holds[0].HostedInvoiceURL != "https://invoice.stripe.com/i/in_1" {
		t.Fatalf("holds = %+v, %v", holds, err)
	}

	c := &ownerContext{account: account, app: app, data: dashboard.ProductControlData{Main: true, AppSlug: app.Slug, BaseURL: "/dashboard/apps/" + app.Slug + "/product-control", Period: "2026-10"}}
	if err := s.ownerOverdue(httptest.NewRequest("GET", "/", nil), c); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if err := dashboard.Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce", dashboard.Page{Title: "Product Control", Body: "product_control", Data: c.data}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"New starts are refused 14 days after an exported invoice&#39;s due date while it is unpaid.",
		"<code>" + tenant.ID + "</code>",
		due.Format(time.DateOnly),
		`href="https://invoice.stripe.com/i/in_1"`,
		"overdue-policy set --block-overdue-after-days N --app " + app.Slug,
	} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("console misses %q", want)
		}
	}
	hold, err := customerOverdueHold(ctx, store, account.ID, app.ID, tenant.ID)
	if err != nil || hold == nil || !hold.DueAt.Equal(due) || hold.HostedInvoiceURL != "https://invoice.stripe.com/i/in_1" {
		t.Fatalf("customer hold = %+v, %v", hold, err)
	}

	call(s.DeleteOverduePolicy, "DELETE", "", http.StatusNoContent)
	if events := host.take(); !slices.Equal(events, []string{"product.billing.overdue_policy_deleted", "product.customer.overdue_unblocked " + tenant.ID}) {
		t.Fatalf("clear audit = %v", events)
	}
	if hold, err := customerOverdueHold(ctx, store, account.ID, app.ID, tenant.ID); err != nil || hold != nil {
		t.Fatalf("hold after clearing = %+v, %v", hold, err)
	}
	if err := s.SyncOverdueOnce(ctx); err != nil || len(host.take()) != 0 {
		t.Fatalf("sync after clearing audited a change: %v", err)
	}
}
