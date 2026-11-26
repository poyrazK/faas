package product

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

type invoiceBatchTestProvider struct {
	t        *testing.T
	keys     map[string]string
	invoices map[string]string
	requests int
	items    int
}

func (p *invoiceBatchTestProvider) RoundTrip(r *http.Request) (*http.Response, error) {
	p.t.Helper()
	reply := func(body string) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/invoices":
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		customer, statement := r.Form.Get("customer"), r.Form.Get("metadata[gregale_statement_id]")
		if (customer != "cus_first" && customer != "cus_second") || statement == "" || r.Form.Get("auto_advance") != "false" {
			p.t.Fatalf("unexpected draft request: %v", r.Form)
		}
		p.requests++
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			p.t.Fatal("draft request omitted its idempotency key")
		}
		if previous, ok := p.keys[statement]; ok && previous != key {
			p.t.Fatal("retry changed a statement's idempotency key")
		}
		p.keys[statement] = key
		invoice := "in_" + strings.TrimPrefix(customer, "cus_")
		if recorded, exists := p.invoices[key]; !exists {
			p.invoices[key] = invoice
			if customer == "cus_first" {
				// Stripe stored the draft before the request timed out.
				return nil, context.DeadlineExceeded
			}
		} else if recorded != invoice {
			p.t.Fatal("different statements shared an idempotency key")
		}
		return reply(fmt.Sprintf(`{"id":%q,"livemode":false,"total":0}`, invoice))
	case r.Method == http.MethodGet && r.URL.Path == "/v1/invoiceitems":
		return reply(`{"data":[],"has_more":false}`)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/invoiceitems":
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		if r.Form.Get("amount") != "900" {
			p.t.Fatalf("unexpected draft item: %v", r.Form)
		}
		p.items++
		return reply(`{"id":"ii_draft"}`)
	default:
		p.t.Fatalf("unexpected provider request %s %s", r.Method, r.URL.Path)
		return nil, context.Canceled
	}
}

func TestProductControlOwnerInvoiceBatch(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "invoice-batch@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "invoice-batch"})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := store.PutProductCatalog(ctx, account.ID, app.ID, api.ProductCatalogSpec{
		Currency: "EUR",
		Features: map[string]api.ProductFeature{
			"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}},
		},
		Plans: map[string]api.ProductPlan{
			"paid": {Label: "Paid", MonthlyFeeMillicents: 900_000, Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 3}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	period := start.Format("2006-01")
	statement := func(ref, name string) invoiceBatchStatement {
		t.Helper()
		tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, ref, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "paid"}); err != nil {
			t.Fatal(err)
		}
		store.SeedProductPlanHistoryForTest(account.ID, app.ID, tenant.ID, catalog.Revision, "paid", true, start.Add(-time.Hour))
		scope := state.ProductBillingScope{AccountID: account.ID, AppID: app.ID, TenantID: tenant.ID, Period: period}
		review, err := store.ReviewProductBilling(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := store.FinalizeProductBilling(ctx, scope, review.ReviewHash)
		if err != nil {
			t.Fatal(err)
		}
		return invoiceBatchStatement{Tenant: tenant.ID, Document: doc.ID}
	}
	first := statement("cus_first", "First workspace")
	second := statement("cus_second", "Second workspace")
	blocked := statement("unmapped", "Missing customer mapping")

	sessions, err := session.NewEphemeralManager(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	provider := &invoiceBatchTestProvider{t: t, keys: map[string]string{}, invoices: map[string]string{}}
	invoices := newOwnerInvoiceTestService(t, store, account.ID, provider)
	var view dashboard.ProductControlData
	svc := Service{
		ReviewInvoice:   invoices.ReviewProductStatementExport,
		ConfirmInvoice:  invoices.ConfirmProductStatementExport,
		DocumentInvoice: invoices.ProductStatementInvoice,
		Host:            customerTestHost{store},
		OwnerSessions:   func() *session.Manager { return sessions },
		OwnerRender: func(w http.ResponseWriter, _ *http.Request, _ state.Account, data dashboard.ProductControlData) {
			view = data
			if err := dashboard.Render(w, slog.Default(), "test", dashboard.Page{Title: "Product Control", Body: "product_control", Data: data}); err != nil {
				t.Fatal(err)
			}
		},
	}
	const origin = "https://gregale.test"
	cookies := map[string]*http.Cookie{}
	call := func(handler func(http.ResponseWriter, *http.Request, state.Account), path string, form url.Values, status int) string {
		t.Helper()
		method := http.MethodGet
		if form != nil {
			method = http.MethodPost
		}
		r := httptest.NewRequest(method, origin+path, strings.NewReader(form.Encode()))
		r.SetPathValue("slug", app.Slug)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler(w, r, account)
		if w.Code != status {
			t.Fatalf("response = %d, want %d: %s", w.Code, status, view.Error)
		}
		for _, cookie := range w.Result().Cookies() {
			cookies[cookie.Name] = cookie
		}
		return w.Body.String()
	}
	checkText := func(body string, values ...string) {
		t.Helper()
		for _, value := range values {
			if !strings.Contains(body, value) {
				t.Fatalf("batch response is missing %q", value)
			}
		}
	}
	const reviewPath, confirmPath = "/invoice-batch/review", "/invoice-batch/confirm"
	monthPath := "/?billing_month=" + period
	body := call(svc.OwnerConsole, monthPath, nil, http.StatusOK)
	checkText(body, "Review selected exports", `name="statement" value="`+first.Tenant+"/"+first.Document, `<option value="stripe">Stripe</option>`)
	if got := view.BillingQueue.Invoices.DraftProviders; len(got) != 1 || got[0].ID != state.BillingProviderStripe {
		t.Fatalf("draft providers = %+v", got)
	}
	form := url.Values{
		"csrf_token": {view.ReviewToken}, "period": {period}, "provider": {"stripe"},
		"statement": {first.Tenant + "/" + first.Document, second.Tenant + "/" + second.Document, blocked.Tenant + "/" + blocked.Document},
	}
	review := func() url.Values {
		t.Helper()
		body := call(svc.OwnerInvoiceBatchReview, reviewPath, form, http.StatusOK)
		checkText(body, "Export 2 invoice drafts", "cus_first", "cus_second", "EUR 9.00", "Test", "set the external_ref")
		if view.InvoiceBatch.Ready != 2 || len(view.InvoiceBatch.Rows) != 3 || view.InvoiceBatch.Rows[2].DocumentID != blocked.Document {
			t.Fatalf("review = %+v", view.InvoiceBatch)
		}
		return url.Values{"csrf_token": {view.InvoiceBatch.Token}, "payload": {view.InvoiceBatch.Payload}}
	}

	post := review()
	if provider.requests != 0 {
		t.Fatal("review attempted to create an invoice")
	}
	call(svc.OwnerInvoiceBatchConfirm, confirmPath, url.Values{"csrf_token": {post.Get("csrf_token")}, "payload": {post.Get("payload") + "A"}}, http.StatusForbidden)
	setOwnerInvoiceTestConnection(t, store, account.ID, state.CollectionSend)
	body = call(svc.OwnerInvoiceBatchConfirm, confirmPath, post, http.StatusOK)
	checkText(body, "Review the export again")
	if provider.requests != 0 {
		t.Fatal("stale or tampered review reached the provider")
	}
	body = call(svc.OwnerInvoiceBatchReview, reviewPath, form, http.StatusOK)
	checkText(body, "This export is set to collect payment")
	if view.InvoiceBatch.Ready != 0 || view.InvoiceBatch.Payload != "" {
		t.Fatalf("send-mode review offered confirmation: %+v", view.InvoiceBatch)
	}
	setOwnerInvoiceTestConnection(t, store, account.ID, state.CollectionNone)
	post = review()

	body = call(svc.OwnerInvoiceBatchConfirm, confirmPath, post, http.StatusOK)
	checkText(body, "in_second", "Retry unfinished exports", "resumes the same draft invoice")
	if provider.requests != 2 || len(provider.invoices) != 2 || provider.items != 1 {
		t.Fatalf("initial confirmation made %d requests, %d drafts, %d items", provider.requests, len(provider.invoices), provider.items)
	}
	retry := url.Values{"csrf_token": {view.InvoiceBatch.Token}, "payload": {view.InvoiceBatch.Payload}}
	body = call(svc.OwnerInvoiceBatchConfirm, confirmPath, retry, http.StatusOK)
	checkText(body, "in_first", "in_second", "#payment-follow-up")
	if view.InvoiceBatch.Token != "" {
		t.Fatal("completed batch offered another retry")
	}
	call(svc.OwnerInvoiceBatchConfirm, confirmPath, retry, http.StatusOK)
	if provider.requests != 3 || len(provider.invoices) != 2 || provider.items != 2 {
		t.Fatalf("replay made %d requests, %d drafts, %d items", provider.requests, len(provider.invoices), provider.items)
	}
	body = call(svc.OwnerConsole, monthPath, nil, http.StatusOK)
	checkText(body, "Missing customer mapping", "Payment follow-up", "Draft to issue")
	pending := view.BillingQueue.Invoices.Items
	if len(pending) != 1 || pending[0].DocumentID != blocked.Document {
		t.Fatalf("awaiting invoice = %+v", pending)
	}
	if len(view.PaymentQueue.Items) != 2 {
		t.Fatalf("payment follow-up = %+v", view.PaymentQueue)
	}
}
