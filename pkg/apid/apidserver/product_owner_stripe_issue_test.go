package apidserver_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

type stripeDraftSend struct {
	mu                                            sync.Mutex
	invoiceID, customer, statement, export, email string
	status, collection, marker                    string
	dueAt                                         *int64
	prepares, finalizes, emails                   int
	sendKeys                                      []string
	sendAt, checkAt                               time.Time
	eventReads                                    int
	eventsForbidden, matchEvent                   bool
	loseFinalizeReply, loseSendReply              bool
	amountRemaining                               int64
	loseReminderReply                             bool
	unexpectedWrites                              int
}

func (f *stripeDraftSend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	invoicePath := "/v1/invoices/" + f.invoiceID
	switch {
	case r.Header.Get("Authorization") != "Bearer rk_test_stripe_reviewed":
		w.WriteHeader(http.StatusUnauthorized)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/invoices":
		_, _ = io.WriteString(w, `{"data":[]}`)
	case r.Method == http.MethodGet && r.URL.Path == invoicePath:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": f.invoiceID, "customer": f.customer, "customer_email": f.email,
			"currency": "eur", "livemode": false, "auto_advance": false,
			"status": f.status, "collection_method": f.collection, "due_date": f.dueAt,
			"total": 900, "amount_due": 900, "amount_remaining": f.amountRemaining, "amount_paid": 900 - f.amountRemaining,
			"metadata": map[string]string{
				"gregale_source": "product_control", "gregale_statement_id": f.statement,
				stripeinvoice.SendExportMetadataKey: f.marker,
			},
		})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/events":
		f.listSendEvents(w, r)
	case r.Method == http.MethodPost && r.URL.Path == invoicePath:
		f.prepares++
		due, err := strconv.ParseInt(r.FormValue("due_date"), 10, 64)
		if err != nil || f.status != "draft" || r.FormValue("auto_advance") != "false" ||
			r.FormValue("collection_method") != "send_invoice" ||
			r.FormValue("metadata["+stripeinvoice.SendExportMetadataKey+"]") != f.export {
			http.Error(w, "unexpected Stripe draft configuration", http.StatusBadRequest)
			return
		}
		f.collection, f.dueAt, f.marker = "send_invoice", &due, f.export
	case r.Method == http.MethodPost && r.URL.Path == invoicePath+"/finalize":
		f.finalizes++
		if f.status != "draft" || f.collection != "send_invoice" || r.FormValue("auto_advance") != "false" {
			http.Error(w, "unexpected Stripe finalize", http.StatusBadRequest)
			return
		}
		f.status = "open"
		// The payment provider can finalize the invoice before the response is lost.
		if f.loseFinalizeReply {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, "{}")
	case r.Method == http.MethodPost && r.URL.Path == invoicePath+"/send":
		key := r.Header.Get("Idempotency-Key")
		if f.status != "open" || key == "" {
			http.Error(w, "unexpected Stripe send", http.StatusBadRequest)
			return
		}
		if !strings.Contains(key, f.export) {
			http.Error(w, "unstable send key", http.StatusBadRequest)
			return
		}
		firstForKey := !slices.Contains(f.sendKeys, key)
		f.sendKeys = append(f.sendKeys, key)
		if firstForKey {
			f.emails++
			// Stripe can accept the send before the client loses the acknowledgment.
			isReminder := strings.HasPrefix(key, "gregale-product-invoice-reminder:")
			if (isReminder && f.loseReminderReply) || (!isReminder && f.loseSendReply) {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
		}
		_, _ = io.WriteString(w, "{}")
	default:
		if r.Method != http.MethodGet {
			f.unexpectedWrites++
		}
		// Reject invoice creation, payment, and every other unreviewed operation.
		http.Error(w, "unexpected Stripe operation: "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
	}
}

func (f *stripeDraftSend) listSendEvents(w http.ResponseWriter, r *http.Request) {
	f.eventReads++
	if f.eventsForbidden {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	if q.Get("type") != "invoice.sent" || q.Get("created[gte]") != strconv.FormatInt(f.sendAt.Unix(), 10) ||
		q.Get("created[lte]") != strconv.FormatInt(f.checkAt.Unix(), 10) || q.Get("limit") != "25" {
		http.Error(w, "unexpected Stripe event query", http.StatusBadRequest)
		return
	}
	event := func(id, kind, invoice string, live bool, at time.Time) map[string]any {
		return map[string]any{
			"id": id, "type": kind, "livemode": live, "created": at.Unix(),
			"data": map[string]any{"object": map[string]any{"object": "invoice", "id": invoice}},
		}
	}
	switch q.Get("starting_after") {
	case "":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				event("evt_wronginvoice", "invoice.sent", "in_elsewhere", false, f.sendAt),
				event("evt_wrongtype", "invoice.finalized", "in_reviewed", false, f.sendAt),
				event("evt_wrongmode", "invoice.sent", "in_reviewed", true, f.sendAt),
				event("evt_earlier", "invoice.sent", "in_reviewed", false, f.sendAt.Add(-time.Second)),
			},
			"has_more": f.matchEvent,
		})
	case "evt_earlier":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":     []map[string]any{event("evt_confirmed", "invoice.sent", "in_reviewed", false, f.sendAt)},
			"has_more": false,
		})
	default:
		http.Error(w, "unexpected Stripe event cursor", http.StatusBadRequest)
	}
}

func TestProductControlOwnerStripeSend(t *testing.T) {
	t.Run("idempotent retry", func(t *testing.T) { testProductControlOwnerStripeSend(t, false) })
	t.Run("check expired send", func(t *testing.T) { testProductControlOwnerStripeSend(t, true) })
}

type stripeOwnerFixture struct {
	store      *state.MemStore
	acct       state.Account
	app        state.App
	catalog    api.ProductCatalog
	owner      string
	now, month time.Time
}

func newStripeOwnerFixture(t *testing.T) *stripeOwnerFixture {
	t.Helper()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	withBillingKeys(t)
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "stripe-issue@example.com", api.PlanPro)
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
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "stripe-issue", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "stripe-issue", Type: state.AppTypeApp})
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
	now := time.Now().UTC().Truncate(time.Second)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	return &stripeOwnerFixture{store: store, acct: acct, app: app, catalog: catalog, owner: owner, now: now, month: month}
}

func (f *stripeOwnerFixture) draft(t *testing.T, name, customer, invoiceID, email string, month time.Time) (state.ProductBillingScope, *stripeDraftSend) {
	t.Helper()
	return f.draftInMode(t, name, customer, invoiceID, email, month, false)
}

func (f *stripeOwnerFixture) draftInMode(t *testing.T, name, customer, invoiceID, email string, month time.Time, live bool) (state.ProductBillingScope, *stripeDraftSend) {
	t.Helper()
	ctx, store, acct, app := t.Context(), f.store, f.acct, f.app
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, customer, name, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "monthly"}); err != nil {
		t.Fatal(err)
	}
	scope := state.ProductBillingScope{AccountID: acct.ID, AppID: app.ID, TenantID: tenant.ID, Period: month.Format("2006-01")}
	store.SeedProductPlanHistoryForTest(acct.ID, app.ID, tenant.ID, f.catalog.Revision, "monthly", true, month.Add(-time.Hour))
	review, err := store.ReviewProductBilling(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.FinalizeProductBilling(ctx, scope, review.ReviewHash)
	if err != nil {
		t.Fatal(err)
	}
	export, _, err := store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{
		AccountID: acct.ID, SourceKind: state.StatementSourceProductControl, StatementID: doc.ID,
		Provider: state.BillingProviderStripe, Customer: customer, Currency: "EUR", AmountMinor: 900, Livemode: live,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatementInvoiceExportInvoice(ctx, acct.ID, export.ID, invoiceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteStatementInvoiceExport(ctx, acct.ID, export.ID); err != nil {
		t.Fatal(err)
	}

	return scope, &stripeDraftSend{invoiceID: invoiceID, customer: customer, statement: doc.ID, export: export.ID,
		email: email, status: "draft", amountRemaining: 900, collection: "charge_automatically", sendAt: f.now, checkAt: f.now.Add(api.ProductStripeInvoiceSendRetryWindow)}
}

type stripeOwnerDashboard struct {
	base    string
	handler http.Handler
	cookies map[string]*http.Cookie
	server  *apidserver.Server
}

func (f *stripeOwnerFixture) dashboard(t *testing.T, provider http.Handler) *stripeOwnerDashboard {
	t.Helper()
	ctx, store, acct := t.Context(), f.store, f.acct
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
	stripeServer := httptest.NewServer(provider)
	t.Cleanup(stripeServer.Close)
	invoices := srv.StatementInvoices()
	invoices.Stripe = &stripeinvoice.Client{BaseURL: stripeServer.URL, HTTP: stripeServer.Client()}
	invoices.Now = func() time.Time { return f.now }
	srv.ProductControl().Now = func() time.Time { return invoices.Now() }

	dashboard := &stripeOwnerDashboard{
		base: "/dashboard/apps/" + f.app.Slug + "/product-control", handler: srv.Handler(), server: srv,
		cookies: map[string]*http.Cookie{apidserver.SessionCookie: {Name: apidserver.SessionCookie, Value: sid}},
	}
	dashboard.connectStripe(t, f.owner, "rk_test_stripe_reviewed")
	return dashboard
}

func (d *stripeOwnerDashboard) connectStripe(t *testing.T, owner, key string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/v1/account/billing-connections/stripe",
		strings.NewReader(`{"secret_key":`+strconv.Quote(key)+`,"collection":"none"}`))
	req.Header.Set("Authorization", "Bearer "+owner)
	w := httptest.NewRecorder()
	d.handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("connect Stripe: %d %s", w.Code, w.Body.String())
	}
}

func (d *stripeOwnerDashboard) call(t *testing.T, path string, form url.Values, status int, wanted ...string) string {
	t.Helper()
	const origin = "https://gregale.dev"
	method := http.MethodGet
	if form != nil {
		method = http.MethodPost
	}
	r := httptest.NewRequest(method, origin+d.base+path, strings.NewReader(form.Encode()))
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range d.cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	d.handler.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != status {
		t.Fatalf("%s %s returned %d, want %d: %s", method, path, w.Code, status, body)
	}
	result := w.Result()
	defer result.Body.Close()
	for _, cookie := range result.Cookies() {
		d.cookies[cookie.Name] = cookie
	}
	for _, text := range wanted {
		if !strings.Contains(body, text) {
			t.Fatalf("%s %s missing %q", method, path, text)
		}
	}
	return body
}

func (f *stripeDraftSend) assertWrites(t *testing.T, prepares, finalizes, sends, emails int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unexpectedWrites != 0 {
		t.Fatalf("Stripe received %d unexpected writes for %s", f.unexpectedWrites, f.invoiceID)
	}
	if f.prepares != prepares || f.finalizes != finalizes || len(f.sendKeys) != sends || f.emails != emails {
		t.Fatalf("Stripe calls for %s prepare=%d finalize=%d send=%d dispatch=%d, want %d %d %d %d",
			f.invoiceID, f.prepares, f.finalizes, len(f.sendKeys), f.emails, prepares, finalizes, sends, emails)
	}
}

func testProductControlOwnerStripeSend(t *testing.T, recoverExpired bool) {
	t.Helper()
	fixture := newStripeOwnerFixture(t)
	_, stripe := fixture.draft(t, "Stripe workspace", "cus_reviewed", "in_reviewed", "first@example.com", fixture.month)
	stripe.loseFinalizeReply, stripe.loseSendReply = true, true
	dashboard := fixture.dashboard(t, stripe)
	now, checkAt, store := fixture.now, stripe.checkAt, fixture.store
	ctx := t.Context()
	call := func(path string, form url.Values, status int, wanted ...string) string {
		t.Helper()
		return dashboard.call(t, path, form, status, wanted...)
	}
	checkProvider := func(prepares, finalizes, sends, emails int) {
		t.Helper()
		stripe.assertWrites(t, prepares, finalizes, sends, emails)
	}

	dueAt := now.AddDate(0, 0, 30).Truncate(24 * time.Hour)
	page := call("", nil, http.StatusOK, "Review and send")
	form := ownerConsoleForm(t, page, "send_stripe_invoice")
	body := call("/review", form, http.StatusOK, "first@example.com", "EUR 9.00", dueAt.Format(time.DateOnly), "Test")
	confirm := ownerConsoleForm(t, body, "confirm")
	stripe.mu.Lock()
	stripe.email = "changed@example.com"
	stripe.mu.Unlock()
	call("/confirm", confirm, http.StatusConflict, "Review the invoice again")
	checkProvider(0, 0, 0, 0)

	body = call("/review", form, http.StatusOK, "changed@example.com")
	confirm = ownerConsoleForm(t, body, "confirm")
	call("/confirm", confirm, http.StatusBadGateway, "Retry send")
	checkProvider(1, 1, 0, 0)
	call("/confirm", confirm, http.StatusBadGateway, "Retry send")
	checkProvider(1, 1, 1, 1)

	// The sync worker can see an open invoice while the send is unconfirmed.
	if _, err := store.RecordStatementInvoicePayment(ctx, stripe.export, state.StatementInvoicePayment{
		Status: state.InvoicePaymentOpen, AmountRemainingMinor: 900, DueAt: &dueAt,
	}, now, nil); err != nil {
		t.Fatal(err)
	}
	page = call("", nil, http.StatusOK, "Stripe send pending", "Review and send")
	form = ownerConsoleForm(t, page, "send_stripe_invoice")
	fixture.now = checkAt
	call("/confirm", confirm, http.StatusConflict, "safe retry window has ended")
	checkProvider(1, 1, 1, 1)
	if recoverExpired {
		path := "/payments/check-stripe-send"
		page := call("", nil, http.StatusOK, "Stripe send needs review", "Check Stripe send", "https://dashboard.stripe.com/test/invoices/in_reviewed")
		checkForm := func(page string) url.Values {
			return ownerConsoleForm(t, page, dashboard.base+path+"#payment-follow-up")
		}
		page = call(path, checkForm(page), http.StatusConflict, "Stripe returned no matching send event", "Stripe send needs review", "Open invoice in Stripe")
		stripe.mu.Lock()
		stripe.eventsForbidden = true
		stripe.mu.Unlock()
		page = call(path, checkForm(page), http.StatusConflict, "Stripe Events access is unavailable", "Stripe send needs review")
		stripe.mu.Lock()
		stripe.eventsForbidden, stripe.matchEvent = false, true
		stripe.mu.Unlock()
		check, replayCookies := checkForm(page), maps.Clone(dashboard.cookies)
		call(path, check, http.StatusOK, "Stripe recorded a send for invoice in_reviewed", "evt_confirmed")
		// Replay the original signed request with its matching cookies.
		dashboard.cookies = replayCookies
		call(path, check, http.StatusOK, "Stripe recorded a send for invoice in_reviewed", "evt_confirmed")
		checkProvider(1, 1, 1, 1)
		stripe.mu.Lock()
		reads := stripe.eventReads
		stripe.mu.Unlock()
		if reads != 4 {
			t.Fatalf("Stripe event requests = %d, want four across two searches and one permission error", reads)
		}
	} else {
		fixture.now = now.Add(time.Second)
		body = call("/review", form, http.StatusOK, "changed@example.com", dueAt.Format(time.DateOnly))
		retry := ownerConsoleForm(t, body, "confirm")
		call("/confirm", retry, http.StatusOK, "Stripe accepted the send for invoice in_reviewed")
		call("/confirm", retry, http.StatusOK, "Stripe accepted the send for invoice in_reviewed")
		checkProvider(1, 1, 2, 1)
	}
	saved, err := store.GetProductStripeInvoiceSend(ctx, fixture.acct.ID, stripe.export)
	if err != nil || saved.Stage != state.StripeInvoiceSent || !saved.DueAt.Equal(dueAt) {
		t.Fatalf("retained Stripe send = %+v, %v", saved, err)
	}
	if recoverExpired && (saved.SendEventID != "evt_confirmed" || !saved.SendEventAt.Equal(now)) {
		t.Fatalf("retained Stripe evidence = %+v", saved)
	}
	if page := call("", nil, http.StatusOK); strings.Contains(page, "Review and send") || strings.Contains(page, "Stripe send pending") ||
		strings.Contains(page, "Stripe send needs review") || strings.Contains(page, "Check Stripe send") {
		t.Fatal("accepted invoice send remains in the follow-up queue")
	}
}
