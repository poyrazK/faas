package product

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlOwnerBillingQueue(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "queue@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "billing-queue"})
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
	period := productcontrol.Period(start)
	workspace := func(ref, name string) string {
		t.Helper()
		tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, ref, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "paid"}); err != nil {
			t.Fatal(err)
		}
		store.SeedProductPlanHistoryForTest(account.ID, app.ID, tenant.ID, catalog.Revision, "paid", true, start.Add(-time.Hour))
		return tenant.ID
	}
	feeOnly := workspace("cus_feeonly", "Fee only workspace")
	unresolved := workspace("unresolved", "Unresolved workspace")
	store.SeedProductOperationForTest(state.Operation{
		AccountID: account.ID, AppID: app.ID, PlatformTenantID: unresolved,
		OperationResponse: api.OperationResponse{ID: uuid.NewString(), State: api.OperationAccepted},
		ProductControl: &api.ProductOperationReceipt{Plan: "paid", Feature: "export", Revision: catalog.Revision, Currency: "EUR",
			Period: period, AdmittedAt: start, UnitPriceMillicents: 10_000},
	})
	sessions, err := session.NewEphemeralManager(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	provider := &ownerInvoiceFakeProvider{t: t}
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
			t.Fatalf("response = %d: %s", w.Code, view.Error)
		}
		for _, cookie := range w.Result().Cookies() {
			cookies[cookie.Name] = cookie
		}
		return w.Body.String()
	}

	body := call(svc.OwnerConsole, "/", nil, http.StatusOK)
	queue := view.BillingQueue
	if queue == nil || queue.Period != period || len(queue.Items) != 2 {
		t.Fatalf("unexpected default queue: %+v", queue)
	}
	if !strings.Contains(body, "Review statement") || !strings.Contains(body, "Review outcomes") {
		t.Fatal("rendered queue omitted a review action")
	}
	for _, item := range queue.Items {
		link, err := url.Parse(item.URL)
		if err != nil || link.Query().Get("tenant") != item.TenantID || link.Query().Get("period") != period || link.Query().Get("billing_month") != period {
			t.Fatalf("bad workspace link: %s, %v", item.URL, err)
		}
		switch item.TenantID {
		case feeOnly:
			if item.PlanFee != ownerMoney("EUR", 900_000) || item.RunCharges != ownerMoney("EUR", 0) || link.Fragment != "billing" {
				t.Fatalf("fee-only row: %+v", item)
			}
		case unresolved:
			if item.Unresolved != 1 || link.Query().Get("runs") != "review" || link.Fragment != "runs" {
				t.Fatalf("unresolved row: %+v", item)
			}
		default:
			t.Fatalf("unexpected row: %+v", item)
		}
	}

	call(svc.OwnerReview, "/", url.Values{"csrf_token": {view.ReviewToken}, "action": {"finalize"}, "tenant": {feeOnly}, "period": {period}}, http.StatusOK)
	if view.Confirmation == nil || view.BillingAmount != ownerMoney("EUR", 900_000) {
		t.Fatalf("unexpected statement review: %+v", view)
	}
	call(svc.OwnerConfirm, "/", url.Values{"csrf_token": {view.Confirmation.Token}, "payload": {view.Confirmation.Payload}}, http.StatusOK)
	call(svc.OwnerConsole, "/?billing_month="+period, nil, http.StatusOK)
	if queue := view.BillingQueue; len(queue.Items) != 1 || queue.Items[0].TenantID != unresolved {
		t.Fatalf("queue after finalization: %+v", queue)
	}
	t.Run("fee-only statement moves into invoicing and a lost provider response is safe to resume", func(t *testing.T) {
		const monthlyPath = "/?billing_month="
		load := func(pending bool) dashboard.ProductControlInvoiceQueueItem {
			t.Helper()
			body := call(svc.OwnerConsole, monthlyPath+period, nil, http.StatusOK)
			queue := view.BillingQueue.Invoices
			if queue == nil || len(queue.Items) != 1 {
				t.Fatalf("expected one invoice row: %+v", queue)
			}
			row := queue.Items[0]
			if row.TenantID != feeOnly || row.Amount != ownerMoney("EUR", 900_000) || row.ExportURL == "" {
				t.Fatalf("unexpected row: %+v", row)
			}
			if pending {
				if row.Provider != "Stripe" || row.HandoffURL != "" || !strings.Contains(body, "Resume export") {
					t.Fatalf("pending export row: %+v", row)
				}
			} else if row.Provider != "" || row.HandoffURL == "" || !strings.Contains(body, "Review export") {
				t.Fatalf("ready export row: %+v", row)
			}
			return row
		}
		review := func(row dashboard.ProductControlInvoiceQueueItem) url.Values {
			t.Helper()
			link, err := url.Parse(row.ExportURL)
			if err != nil {
				t.Fatal(err)
			}
			body := call(svc.OwnerConsole, link.RequestURI(), nil, http.StatusOK)
			doc := link.Query().Get("document")
			if len(view.Documents) != 1 || view.Documents[0].ID != doc || !strings.Contains(body, link.Fragment) {
				t.Fatalf("invoice link did not open the export form: %s", row.ExportURL)
			}
			call(svc.OwnerReview, "/", url.Values{
				"csrf_token": {view.ReviewToken}, "action": {"export_invoice"}, "tenant": {feeOnly},
				"period": {period}, "document": {doc}, "provider": {"stripe"},
			}, http.StatusOK)
			if !strings.Contains(view.Confirmation.Title, "Invoice export") {
				t.Fatalf("unexpected review: %+v", view.Confirmation)
			}
			return url.Values{"csrf_token": {view.Confirmation.Token}, "payload": {view.Confirmation.Payload}}
		}

		// A connection change after review must not send an invoice under
		// newly selected collection settings.
		post := review(load(false))
		setOwnerInvoiceTestConnection(t, store, account.ID, state.CollectionSend)
		call(svc.OwnerConfirm, "/", post, http.StatusConflict)
		if provider.requests != 0 {
			t.Fatal("stale confirmation reached the provider")
		}
		setOwnerInvoiceTestConnection(t, store, account.ID, state.CollectionNone)
		post = review(load(false))
		if provider.requests != 0 {
			t.Fatal("review reached the provider")
		}
		call(svc.OwnerConfirm, "/", post, http.StatusBadGateway)
		if provider.created != 1 || provider.requests != 1 {
			t.Fatalf("lost response left %d invoices after %d requests", provider.created, provider.requests)
		}
		post = review(load(true))
		body := call(svc.OwnerConfirm, "/", post, http.StatusOK)
		if !strings.Contains(body, "Invoice in_feeonly recorded") {
			t.Fatalf("resume did not report the invoice: %s", view.Notice)
		}
		call(svc.OwnerConfirm, "/", post, http.StatusOK)
		call(svc.OwnerConsole, monthlyPath+period, nil, http.StatusOK)
		if len(view.BillingQueue.Invoices.Items) != 0 || provider.created != 1 || provider.requests != 2 || provider.amount != strconv.Itoa(900) {
			t.Fatalf("after retry: queue %+v, created %d, requests %d, amount %q", view.BillingQueue.Invoices, provider.created, provider.requests, provider.amount)
		}
	})
	t.Run("payment follow-up uses provider status across months without overdue holds", func(t *testing.T) {
		clock := time.Now().UTC().Add(time.Minute)
		svc.Now = func() time.Time { return clock }
		invoices.Now = svc.Now
		otherMonth := productcontrol.Period(start.AddDate(0, -1, 0))
		load := func(status string) (dashboard.ProductControlPaymentQueueItem, string) {
			t.Helper()
			body := call(svc.OwnerConsole, "/?billing_month="+otherMonth, nil, http.StatusOK)
			queue := view.PaymentQueue
			if view.BillingQueue.Period != otherMonth || queue == nil || !strings.Contains(body, "Payment follow-up") {
				t.Fatalf("payment queue = %+v, billing month %q", queue, view.BillingQueue.Period)
			}
			if status == "" {
				if len(queue.Items) != 0 || !strings.Contains(body, "No invoices need payment follow-up") {
					t.Fatalf("paid invoice remained in the payment queue: %+v", queue)
				}
				return dashboard.ProductControlPaymentQueueItem{}, body
			}
			if len(queue.Items) != 1 || queue.Items[0].Action != status || !strings.Contains(body, ">"+status+"</td>") {
				t.Fatalf("payment queue = %+v, want %s", queue, status)
			}
			row := queue.Items[0]
			if row.TenantID != feeOnly || row.Name != "Fee only workspace" || row.Period != period {
				t.Fatalf("payment row did not retain workspace and statement month: %+v", row)
			}
			return row, body
		}
		draft, _ := load("Draft to issue")
		if draft.Remaining != "Unknown" || draft.LastSync != "Never" {
			t.Fatalf("unsynced draft showed observed details: %+v", draft)
		}
		link, err := url.Parse(draft.DocumentURL)
		if err != nil {
			t.Fatal(err)
		}
		body := call(svc.OwnerConsole, link.RequestURI(), nil, http.StatusOK)
		if len(view.Documents) != 1 || view.TenantID != feeOnly || view.Period != period ||
			view.Documents[0].ID != link.Query().Get("document") || !strings.Contains(body, `id="`+link.Fragment+`"`) {
			t.Fatalf("payment link did not open the scoped invoice: %s", draft.DocumentURL)
		}
		export, err := store.GetStatementInvoiceExport(ctx, account.ID, state.StatementSourceProductControl, view.Documents[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		sync := func(payment string) {
			t.Helper()
			provider.payment = payment
			if err := store.DeferStatementInvoiceSync(ctx, export.ID, clock); err != nil {
				t.Fatal(err)
			}
			if err := invoices.SyncOnce(ctx); err != nil {
				t.Fatal(err)
			}
		}
		due := clock.Add(-time.Hour)
		sync(fmt.Sprintf(`{"id":"in_feeonly","status":"open","amount_paid":200,"amount_remaining":700,"due_date":%d,"hosted_invoice_url":"https://invoice.stripe.com/i/test"}`, due.Unix()))
		overdue, body := load("Overdue")
		if view.Overdue == nil || len(view.Overdue.Holds) != 0 || !strings.Contains(view.Overdue.Policy, "No overdue policy") ||
			overdue.Remaining != "EUR 7.00" || overdue.DueAt != due.Format(time.DateOnly) ||
			overdue.LastSync != clock.Format("2006-01-02 15:04 UTC") || !strings.Contains(body, overdue.HostedURL) || overdue.HostedURL == "" {
			t.Fatalf("overdue row = %+v, holds = %+v", overdue, view.Overdue)
		}
		clock = clock.Add(api.ConsumerOverdueStatusMaxAge + time.Minute)
		sync("")
		stale, _ := load("Check payment status")
		if stale.LastSync != overdue.LastSync {
			t.Fatalf("failed provider sync advanced the last successful sync: %+v", stale)
		}
		sync(`{"id":"in_feeonly","status":"paid","amount_paid":900,"amount_remaining":0}`)
		load("")
	})
	call(svc.OwnerConsole, "/?billing_month="+productcontrol.Period(now), nil, http.StatusBadRequest)
}
