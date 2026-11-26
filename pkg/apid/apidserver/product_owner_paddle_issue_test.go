package apidserver_test

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/paddleinvoice"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlOwnerPaddleIssue(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	withBillingKeys(t)
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "paddle-issue@example.com", api.PlanPro)
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
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "paddle-issue", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "paddle-issue", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := store.PutProductCatalog(ctx, acct.ID, app.ID, api.ProductCatalogSpec{
		Currency: "EUR",
		Features: map[string]api.ProductFeature{
			"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}},
		},
		Plans: map[string]api.ProductPlan{
			"monthly": {Label: "Monthly", MonthlyFeeMillicents: 900_000, Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	const customer = "ctm_01issue"
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, customer, "Paddle workspace", 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "monthly"}); err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Now().UTC().Truncate(time.Second)
	month := time.Date(issuedAt.Year(), issuedAt.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	scope := state.ProductBillingScope{AccountID: acct.ID, AppID: app.ID, TenantID: tenant.ID, Period: month.Format("2006-01")}
	store.SeedProductPlanHistoryForTest(acct.ID, app.ID, tenant.ID, catalog.Revision, "monthly", true, month.Add(-time.Hour))
	review, err := store.ReviewProductBilling(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.FinalizeProductBilling(ctx, scope, review.ReviewHash)
	if err != nil {
		t.Fatal(err)
	}

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
	const checkoutURL = "https://checkout.example.com/pay?_ptxn=txn_1"
	paddle := &fakePaddle{
		checkoutURL: checkoutURL,
		addresses:   map[string]string{}, totalMinor: "900", billedAt: &issuedAt,
		totalByAddress: map[string]string{"add_first": "1080", "add_changed": "1170"},
	}
	paddleServer := httptest.NewServer(paddle)
	t.Cleanup(paddleServer.Close)
	srv.StatementInvoices().Paddle = &paddleinvoice.Client{BaseURL: paddleServer.URL, HTTP: paddleServer.Client()}
	handler := srv.Handler()
	req := httptest.NewRequest(http.MethodPut, "/v1/account/billing-connections/paddle",
		strings.NewReader(`{"secret_key":"pdl_sdbx_apikey_01abcdWXYZ","collection":"none"}`))
	req.Header.Set("Authorization", "Bearer "+owner)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("connect Paddle: %d %s", w.Code, w.Body.String())
	}
	export, problem := srv.StatementInvoices().ExportProductStatement(ctx, acct, scope, doc.ID, state.BillingProviderPaddle, "")
	if problem != nil || export.InvoiceID != "txn_1" || export.PaymentStatus != state.InvoicePaymentDraft {
		t.Fatalf("draft export = %+v, %v", export, problem)
	}

	const origin = "https://gregale.dev"
	base := "/dashboard/apps/" + app.Slug + "/product-control"
	cookies := map[string]*http.Cookie{apidserver.SessionCookie: {Name: apidserver.SessionCookie, Value: sid}}
	call := func(path string, form url.Values, status int) string {
		t.Helper()
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
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		result := w.Result()
		defer func() { _ = result.Body.Close() }()
		for _, cookie := range result.Cookies() {
			cookies[cookie.Name] = cookie
		}
		return w.Body.String()
	}
	expect := func(body string, wanted ...string) {
		t.Helper()
		for _, text := range wanted {
			if !strings.Contains(body, text) {
				t.Fatalf("missing %q in owner response", text)
			}
		}
	}
	checkProvider := func(bills int, address string) {
		t.Helper()
		paddle.mu.Lock()
		defer paddle.mu.Unlock()
		if len(paddle.created) != 1 || paddle.billAttempts != bills || paddle.billed[export.InvoiceID] != address {
			t.Fatalf("Paddle saw %d creations and %d bills, addresses %v", len(paddle.created), paddle.billAttempts, paddle.billed)
		}
	}
	page := call(base, nil, http.StatusOK)
	expect(page, "Paddle workspace", "Draft to issue", "Review and issue")
	reviewForm := ownerConsoleForm(t, page, "issue_invoice")
	if reviewForm.Get("document") != doc.ID || reviewForm.Get("period") != scope.Period {
		t.Fatalf("issue form = %v", reviewForm)
	}
	body := call(base+"/review", reviewForm, http.StatusUnprocessableEntity)
	expect(body, "Add an active billing address", "Return to payment follow-up")
	checkProvider(0, "")

	paddle.mu.Lock()
	paddle.addresses[customer] = "add_first"
	paddle.mu.Unlock()
	body = call(base+"/review", reviewForm, http.StatusOK)
	expect(body, "Issue and send invoice", customer, "EUR 10.80", "Due 30 days after issuance", "Test")
	checkProvider(0, "")
	prepared, problem := srv.StatementInvoices().RefreshProductInvoicePayment(ctx, acct, scope, doc.ID)
	if problem != nil || prepared.PaymentStatus != state.InvoicePaymentDraft || prepared.HostedInvoiceURL != "" {
		t.Fatalf("unissued checkout exposed: %+v, %v", prepared, problem)
	}
	confirm := ownerConsoleForm(t, body, "confirm")
	paddle.mu.Lock()
	paddle.addresses[customer] = "add_changed"
	paddle.mu.Unlock()
	body = call(base+"/confirm", confirm, http.StatusConflict)
	expect(body, "billing details, or connection changed")
	checkProvider(0, "")

	body = call(base+"/review", reviewForm, http.StatusOK)
	expect(body, "Invoice total", "EUR 11.70")
	confirm = ownerConsoleForm(t, body, "confirm")
	paddle.mu.Lock()
	paddle.loseBillReply = true
	paddle.mu.Unlock()
	body = call(base+"/confirm", confirm, http.StatusBadGateway)
	expect(body, "Retry issuance", "check the existing Paddle invoice")
	checkProvider(1, "add_changed")

	retry := ownerConsoleForm(t, body, "confirm")
	body = call(base+"/confirm", retry, http.StatusOK)
	expect(body, "Paddle invoice txn_1 is issued.", "Return to payment follow-up")
	checkProvider(1, "add_changed")
	body = call(base+"/confirm", retry, http.StatusOK)
	expect(body, "Paddle invoice txn_1 is issued.")
	checkProvider(1, "add_changed")

	synced, err := store.GetStatementInvoiceExport(ctx, acct.ID, state.StatementSourceProductControl, doc.ID)
	if err != nil || synced.PaymentStatus != state.InvoicePaymentOpen || synced.StatusSyncedAt == nil ||
		synced.DueAt == nil || !synced.DueAt.Equal(issuedAt.AddDate(0, 0, 30)) ||
		synced.AmountRemainingMinor == nil || *synced.AmountRemainingMinor != 1170 ||
		synced.ProviderInvoiceID != export.InvoiceID || synced.HostedInvoiceURL != checkoutURL {
		t.Fatalf("issued payment status = %+v, %v", synced, err)
	}
	// A background sync that saw the draft before issuance cannot put it back.
	if _, err := store.RecordStatementInvoicePaymentIfFresh(ctx, synced.ID, state.StatementInvoicePayment{Status: state.InvoicePaymentDraft},
		synced.StatusSyncedAt.Add(-time.Second), nil); !errors.Is(err, state.ErrStatementInvoicePaymentStale) {
		t.Fatalf("older draft observation returned %v", err)
	}
	page = call(base, nil, http.StatusOK)
	paddle.mu.Lock()
	preparations := paddle.prepareAttempts
	paddle.mu.Unlock()
	if preparations != 2 {
		t.Fatalf("Paddle prepared %d drafts, want two reviews", preparations)
	}
	if strings.Contains(page, "Review and issue") || strings.Contains(page, "Draft to issue") {
		t.Fatal("issued invoice remained in the draft queue")
	}
}
