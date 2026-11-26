package apidserver_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlOwnerPaymentRefresh(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	withBillingKeys(t)
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "payment-refresh@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountEmailVerified(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	owner, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "payment-refresh", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "payment-refresh", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProductCatalog(ctx, acct.ID, app.ID, api.ProductCatalogSpec{
		Currency: "USD",
		Features: map[string]api.ProductFeature{
			"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}},
		},
		Plans: map[string]api.ProductPlan{
			"paid": {Label: "Paid", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 3}}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0).Format("2006-01")
	due := now.Add(-time.Hour)
	previous := now.Add(-time.Hour)
	statement := func(name, invoice string, checked time.Time) api.ProductBillingDocument {
		t.Helper()
		tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		doc := api.ProductBillingDocument{
			ID: uuid.NewString(), Kind: "statement", TenantID: tenant.ID, Period: month, Currency: "USD", AmountMillicents: 700_000,
			Snapshot: &api.ProductBillingReview{TenantID: tenant.ID, Period: month, Currency: "USD", Closed: true, AmountMillicents: 700_000},
		}
		store.SeedProductBillingDocumentForTest(acct.ID, app.ID, doc)
		export, _, err := store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{
			AccountID: acct.ID, SourceKind: state.StatementSourceProductControl, StatementID: doc.ID,
			Provider: state.BillingProviderStripe, Customer: "cus_refresh", Currency: doc.Currency, AmountMinor: 700,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetStatementInvoiceExportInvoice(ctx, acct.ID, export.ID, invoice); err != nil {
			t.Fatal(err)
		}
		scope := state.ProductBillingScope{AccountID: acct.ID, AppID: app.ID, TenantID: tenant.ID, Period: month}
		if _, err := store.HandoffProductBilling(ctx, scope, doc.ID, invoice); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CompleteStatementInvoiceExport(ctx, acct.ID, export.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RecordStatementInvoicePayment(ctx, export.ID, state.StatementInvoicePayment{
			Status: state.InvoicePaymentOpen, DueAt: &due, AmountRemainingMinor: 700,
		}, checked, nil); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	paid := statement("Newly paid workspace", "in_paid", now.Add(-api.ConsumerOverdueStatusMaxAge-time.Hour))
	downtime := statement("Provider outage workspace", "in_outage", previous)

	started, unblock := make(chan struct{}), make(chan struct{})
	var release sync.Once
	releaseProvider := func() { release.Do(func() { close(unblock) }) }
	var paidReads, failedReads atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer rk_test_") {
			t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/v1/invoices":
			_, _ = io.WriteString(w, `{"data":[],"has_more":false}`)
		case "/v1/invoices/in_paid":
			if paidReads.Add(1) == 1 {
				close(started)
				select {
				case <-unblock:
				case <-r.Context().Done():
					return
				}
			}
			_, _ = fmt.Fprintf(w, `{"status":"paid","currency":"usd","amount_paid":700,"amount_remaining":0,"status_transitions":{"paid_at":%d}}`, now.Unix())
		case "/v1/invoices/in_outage":
			failedReads.Add(1)
			w.WriteHeader(http.StatusBadGateway)
		default:
			t.Errorf("unexpected invoice read %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(provider.Close)
	t.Cleanup(releaseProvider)

	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := apidserver.MintDashboardSession(ctx, store, mgr, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}, "",
		apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*time.Minute, "").WithProductControlEnabled(true)
	srv.StatementInvoices().Stripe = &stripeinvoice.Client{BaseURL: provider.URL, HTTP: provider.Client()}
	handler := srv.Handler()
	req := httptest.NewRequest(http.MethodPut, "/v1/account/billing-connections/stripe", strings.NewReader(`{"secret_key":"rk_test_51abcdWXYZ"}`))
	req.Header.Set("Authorization", "Bearer "+owner)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("connect Stripe: %d %s", w.Code, w.Body.String())
	}

	const origin = "https://gregale.dev"
	base := "/dashboard/apps/" + app.Slug + "/product-control"
	refreshPath := base + "/payments/refresh"
	cookies := map[string]*http.Cookie{apidserver.SessionCookie: {Name: apidserver.SessionCookie, Value: sid}}
	request := func(path string, form url.Values) *http.Request {
		method := http.MethodGet
		if form != nil {
			method = http.MethodPost
		}
		r := httptest.NewRequest(method, origin+path, strings.NewReader(form.Encode()))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		return r
	}
	read := func(w *httptest.ResponseRecorder, status int) string {
		t.Helper()
		if w.Code != status {
			t.Fatalf("response %d, want %d: %s", w.Code, status, w.Body.String())
		}
		result := w.Result()
		defer func() { _ = result.Body.Close() }()
		for _, cookie := range result.Cookies() {
			cookies[cookie.Name] = cookie
		}
		return w.Body.String()
	}
	call := func(path string, form url.Values, status int) string {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, request(path, form))
		return read(w, status)
	}
	expect := func(body string, texts ...string) {
		t.Helper()
		for _, text := range texts {
			if !strings.Contains(body, text) {
				t.Fatalf("missing %q in follow-up response", text)
			}
		}
	}
	formFor := func(body string, doc api.ProductBillingDocument) url.Values {
		t.Helper()
		form := ownerConsoleForm(t, body, refreshPath+"#payment-follow-up")
		form.Set("document", doc.ID)
		form.Set("tenant", doc.TenantID)
		return form
	}
	getExport := func(doc api.ProductBillingDocument) state.StatementInvoiceExport {
		t.Helper()
		export, err := store.GetStatementInvoiceExport(ctx, acct.ID, state.StatementSourceProductControl, doc.ID)
		if err != nil {
			t.Fatal(err)
		}
		return export
	}

	page := call(base, nil, http.StatusOK)
	expect(page, "Check payment status", "Overdue", "Refresh payment status")
	invalid := formFor(page, paid)
	invalid.Del("csrf_token")
	call(refreshPath, invalid, http.StatusForbidden)
	if paidReads.Load() != 0 {
		t.Fatal("refresh without CSRF reached Stripe")
	}
	page = call(base, nil, http.StatusOK)
	unknown := formFor(page, paid)
	unknown.Set("document", uuid.NewString())
	page = call(refreshPath, unknown, http.StatusNotFound)
	expect(page, "no such Product Control statement", "Newly paid workspace")
	if paidReads.Load() != 0 {
		t.Fatal("refresh for an unrelated statement reached Stripe")
	}
	conn, err := store.GetBillingConnection(ctx, acct.ID, state.BillingProviderStripe)
	if err != nil {
		t.Fatal(err)
	}
	live := conn
	live.Livemode = true
	if _, err := store.PutBillingConnection(ctx, live); err != nil {
		t.Fatal(err)
	}
	page = call(refreshPath, formFor(page, paid), http.StatusConflict)
	expect(page, "Reconnect Stripe in test mode", "Check payment status", "USD 7.00")
	if paidReads.Load() != 0 {
		t.Fatal("refresh with the wrong provider mode reached Stripe")
	}
	if _, err := store.PutBillingConnection(ctx, conn); err != nil {
		t.Fatal(err)
	}

	page = call(refreshPath, formFor(page, downtime), http.StatusBadGateway)
	expect(page, "Could not confirm payment status with Stripe", "Provider outage workspace", "Overdue", "USD 7.00",
		previous.Format("2006-01-02 15:04 UTC"))
	saved := getExport(downtime)
	if saved.PaymentStatus != state.InvoicePaymentOpen || saved.StatusSyncedAt == nil || !saved.StatusSyncedAt.Equal(previous) ||
		saved.AmountRemainingMinor == nil || *saved.AmountRemainingMinor != 700 || failedReads.Load() != 1 {
		t.Fatalf("failed refresh changed the payment observation: %+v, %d reads", saved, failedReads.Load())
	}
	page = call(refreshPath, formFor(page, downtime), http.StatusConflict)
	if failedReads.Load() != 1 {
		t.Fatal("retry repeated the failed provider request")
	}

	form := formFor(page, paid)
	first, completed := httptest.NewRecorder(), make(chan struct{})
	firstRequest := request(refreshPath, form)
	go func() {
		defer close(completed)
		handler.ServeHTTP(first, firstRequest)
	}()
	select {
	case <-started:
	case <-completed:
		t.Fatalf("refresh stopped before contacting Stripe: %d %s", first.Code, first.Body.String())
	case <-time.After(10 * time.Second):
		t.Fatal("refresh did not reach Stripe")
	}
	duplicate := call(refreshPath, form, http.StatusConflict)
	expect(duplicate, "A payment refresh or invoice update is already in progress", "Newly paid workspace")
	releaseProvider()
	select {
	case <-completed:
	case <-time.After(10 * time.Second):
		t.Fatal("first refresh did not complete")
	}
	page = read(first, http.StatusOK)
	expect(page, "Payment confirmed for invoice in_paid.", "Provider outage workspace")
	if strings.Contains(page, "Newly paid workspace") || paidReads.Load() != 1 {
		t.Fatalf("paid invoice remained in follow-up or was read %d times", paidReads.Load())
	}
	saved = getExport(paid)
	if saved.PaymentStatus != state.InvoicePaymentPaid || saved.StatusSyncedAt == nil || !saved.StatusSyncedAt.After(previous) {
		t.Fatalf("provider payment was not recorded: %+v", saved)
	}
	page = call(refreshPath, formFor(page, paid), http.StatusOK)
	expect(page, "Payment confirmed for invoice in_paid.")
	if paidReads.Load() != 1 {
		t.Fatal("replaying a confirmed payment repeated the provider request")
	}
}
