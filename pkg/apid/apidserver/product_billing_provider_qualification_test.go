package apidserver_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/statementinvoices"
	"github.com/onebox-faas/faas/pkg/billing/lagoinvoice"
	"github.com/onebox-faas/faas/pkg/billing/paddleinvoice"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// Product Control provider qualification (ADR-20261018212918). Each provider
// whose test credentials are set runs the owner flow against its real API
// through apid's routes and clients; with none set the test skips, so it
// stays compiled in CI without reaching the network:
//
//	FAAS_QUALIFY_STRIPE_TEST_KEY=sk_test_…        Stripe test mode
//	FAAS_QUALIFY_PADDLE_SANDBOX_KEY=pdl_sdbx_apikey_…   Paddle sandbox
//	FAAS_QUALIFY_LAGO_KEY=… FAAS_QUALIFY_LAGO_URL=https://api.getlago.com \
//	  FAAS_QUALIFY_LAGO_TEST_ORG=1                 a Lago organization kept for tests
//	FAAS_QUALIFY_EVIDENCE_DIR=…                    optional: one JSON record per provider
//
// make product-control-provider-qualification runs it. Lago has no test
// mode, so its run needs the explicit test-organization acknowledgement.
// Each run leaves one customer and one settled or cancelled invoice behind.
//
// Gregale only calls the providers through apid. The test calls them
// directly only for what a customer or the owner would do in the provider:
// creating the customer, and paying or cancelling the invoice.

const qualificationAmountMillicents = 5 * 250000 // five successful exports at EUR 2.50

// qualifyProvider is one provider's qualification.
type qualifyProvider struct {
	name       string
	connection api.PutBillingConnectionRequest
	// customer creates a customer in the provider's test environment.
	customer func(t *testing.T, label string) string
	// beforeExport makes the customer billable; it may export once.
	beforeExport func(t *testing.T, q *qualification)
	// afterExport checks the provider's own copy of the invoice.
	afterExport func(t *testing.T, q *qualification, invoiceID string)
	// settle pays the invoice outside Gregale, or cancels it where the
	// provider offers no API payment, and returns the payment status
	// Gregale must read next.
	settle func(t *testing.T, invoiceID string) string
	// hostedURL reports whether the provider must give customers a page
	// to pay the invoice.
	hostedURL bool
}

// qualification is one provider run: an owner account with one Product
// Control customer and one finalized statement for last month.
type qualification struct {
	handler  http.Handler
	invoices *statementinvoices.Service
	// syncAt and holdAt run the payment sync and overdue hold workers once
	// at a given time, so a fresh invoice can be made overdue.
	syncAt   func(time.Time) error
	holdAt   func(time.Time) error
	owner    string
	account  state.Account
	app      string
	tenant   string
	customer string
	period   string
	doc      api.ProductBillingDocument
	provider string
	evidence map[string]any
	checks   []string
}

// adr: 20261018212918 — with a provider's test credentials, a Product Control
// statement exports to the provider, its payment status syncs, an overdue
// policy holds the customer and the hold lifts once the provider reports the
// invoice paid or cancelled; Paddle also takes the customer's billing details
// and bills the transaction with them (ADR-20261020120000).
func TestProductControlProviderQualification(t *testing.T) {
	providers := qualificationProviders(t)
	if len(providers) == 0 {
		t.Skip("no provider test credentials (FAAS_QUALIFY_STRIPE_TEST_KEY, FAAS_QUALIFY_PADDLE_SANDBOX_KEY, FAAS_QUALIFY_LAGO_KEY); skipping live provider qualification")
	}
	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) { runProviderQualification(t, p) })
	}
}

func qualificationProviders(t *testing.T) []qualifyProvider {
	t.Helper()
	var out []qualifyProvider
	if key := os.Getenv("FAAS_QUALIFY_STRIPE_TEST_KEY"); key != "" {
		if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "rk_test_") {
			t.Fatal("FAAS_QUALIFY_STRIPE_TEST_KEY must be a Stripe test-mode key (sk_test_… or rk_test_…)")
		}
		out = append(out, stripeQualification(key))
	}
	if key := os.Getenv("FAAS_QUALIFY_PADDLE_SANDBOX_KEY"); key != "" {
		if live, err := paddleinvoice.KeyLivemode(key); err != nil || live {
			t.Fatal("FAAS_QUALIFY_PADDLE_SANDBOX_KEY must be a Paddle sandbox key (pdl_sdbx_apikey_…)")
		}
		out = append(out, paddleQualification(key))
	}
	if key := os.Getenv("FAAS_QUALIFY_LAGO_KEY"); key != "" {
		base, err := lagoinvoice.NormalizeURL(os.Getenv("FAAS_QUALIFY_LAGO_URL"))
		if err != nil || os.Getenv("FAAS_QUALIFY_LAGO_URL") == "" || os.Getenv("FAAS_QUALIFY_LAGO_TEST_ORG") != "1" {
			t.Fatal("Lago has no test mode: set FAAS_QUALIFY_LAGO_URL to the API URL of a Lago organization kept for tests and FAAS_QUALIFY_LAGO_TEST_ORG=1")
		}
		out = append(out, lagoQualification(key, base))
	}
	return out
}

func runProviderQualification(t *testing.T, p qualifyProvider) {
	q := newQualification(t, p)
	if p.beforeExport != nil {
		p.beforeExport(t, q)
	}
	first := q.export(t, http.StatusOK)
	if first.Status != state.StatementInvoiceExportCompleted || first.InvoiceID == "" || first.AmountMinor != qualificationAmountMillicents/1000 {
		t.Fatalf("export = %+v", first)
	}
	if again := q.export(t, http.StatusOK); again.InvoiceID != first.InvoiceID {
		t.Fatalf("a repeated export made invoice %s besides %s", again.InvoiceID, first.InvoiceID)
	}
	q.pass("export completed once; a repeated export returned the same invoice")
	q.evidence["invoice_id"], q.evidence["dashboard_url"] = first.InvoiceID, first.DashboardURL
	if p.afterExport != nil {
		p.afterExport(t, q, first.InvoiceID)
	}

	open := q.syncedExport(t, time.Now().UTC().Add(time.Minute))
	q.checkOpen(t, p, open)
	q.checkOverdueHold(t, p, first.InvoiceID, open)
	q.writeEvidence(t)
}

func newQualification(t *testing.T, p qualifyProvider) *qualification {
	t.Helper()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	withBillingKeys(t)
	ctx := t.Context()
	label := "Gregale qualification " + time.Now().UTC().Format("2006-01-02 15:04:05")
	customer := p.customer(t, label)
	mem := state.NewMemStore()
	account, err := mem.CreateAccount(ctx, "provider-qualification-"+p.name+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	owner, hash, _ := api.GenerateAPIKey()
	if _, err := mem.CreateAPIKey(ctx, account.ID, hash, "owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "qualify-" + p.name})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}},
		Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 250000}}}}}
	if _, err := mem.PutProductCatalog(ctx, account.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := mem.CreatePlatformTenant(ctx, account.ID, customer, label, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	period := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0).Format("2006-01")
	doc := api.ProductBillingDocument{ID: uuid.NewString(), Kind: "statement", TenantID: tenant.ID, Period: period, Currency: "EUR", AmountMillicents: qualificationAmountMillicents,
		Snapshot: &api.ProductBillingReview{TenantID: tenant.ID, Period: period, Currency: "EUR", Closed: true, Starts: 5, AmountMillicents: qualificationAmountMillicents,
			Lines: []api.ProductBillingLine{{Feature: "export", Plan: "starter", UnitPriceMillicents: 250000, Starts: 5, Succeeded: 5, AmountMillicents: qualificationAmountMillicents}}}}
	// The overdue sync reads the statement from the store; the routes read
	// it, for a closed month, from the fake.
	mem.SeedProductBillingDocumentForTest(account.ID, app.ID, doc)
	fake := &productStatementFake{MemStore: mem, docs: map[string]api.ProductBillingDocument{doc.ID: doc}, handoffs: map[string]api.ProductInvoiceHandoff{}}
	srv := apidserver.NewServer(statetest.NewMem(fake), slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{})
	*srv.Mailer() = &apidserver.RecordingMailer{}
	srv.WithProductControlEnabled(true)
	invoices, products := srv.StatementInvoices(), srv.ProductControl()
	q := &qualification{handler: srv.Handler(), invoices: invoices, owner: owner, account: account, app: app.Slug, tenant: tenant.ID, customer: customer,
		period: period, doc: doc, provider: p.name, evidence: map[string]any{"provider": p.name, "ran_at": now, "customer": customer}}
	q.syncAt = func(at time.Time) error {
		invoices.Now = func() time.Time { return at }
		return invoices.SyncOnce(ctx)
	}
	q.holdAt = func(at time.Time) error {
		products.Now = func() time.Time { return at }
		return products.SyncOverdueOnce(ctx)
	}
	conn := q.call(t, "PUT", "/v1/account/billing-connections/"+p.name, p.connection, http.StatusOK)
	var connected api.BillingConnectionResponse
	if err := json.Unmarshal(conn.Body.Bytes(), &connected); err != nil || (connected.Livemode && p.name != state.BillingProviderLago) {
		t.Fatalf("connection = %s, %v; qualification never uses live keys", conn.Body, err)
	}
	q.pass("connection accepted the test key")
	return q
}

func (q *qualification) call(t *testing.T, method, path string, body any, status int) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+q.owner)
	w := httptest.NewRecorder()
	q.handler.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	return w
}

func (q *qualification) invoicePath() string {
	return "/v1/apps/" + q.app + "/product/billing/documents/" + q.doc.ID + "/invoice?tenant_id=" + q.tenant + "&period=" + q.period
}

// export exports the statement and returns the export, or the response
// body as a failed export's detail.
func (q *qualification) export(t *testing.T, status int) api.StatementInvoiceExportResponse {
	t.Helper()
	w := q.call(t, "POST", q.invoicePath(), api.ProductInvoiceExportRequest{TenantID: q.tenant, Provider: q.provider}, status)
	var out api.StatementInvoiceExportResponse
	if status == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func (q *qualification) exportStatus(t *testing.T) api.StatementInvoiceExportResponse {
	t.Helper()
	var out api.StatementInvoiceExportResponse
	if err := json.Unmarshal(q.call(t, "GET", q.invoicePath(), nil, http.StatusOK).Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// syncedExport runs the payment sync worker at a given time and returns the
// export it updated.
func (q *qualification) syncedExport(t *testing.T, at time.Time) api.StatementInvoiceExportResponse {
	t.Helper()
	if err := q.syncAt(at); err != nil {
		t.Fatal(err)
	}
	return q.exportStatus(t)
}

// holds runs the overdue hold worker at a given time and returns the app's
// holds.
func (q *qualification) holds(t *testing.T, at time.Time) []api.ProductOverdueHold {
	t.Helper()
	if err := q.holdAt(at); err != nil {
		t.Fatal(err)
	}
	var out api.ProductOverdueHolds
	if err := json.Unmarshal(q.call(t, "GET", "/v1/apps/"+q.app+"/product/billing/overdue-holds", nil, http.StatusOK).Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Holds
}

// checkOpen checks the synced payment status of the new invoice, on the
// export and on the owner's statement.
func (q *qualification) checkOpen(t *testing.T, p qualifyProvider, open api.StatementInvoiceExportResponse) {
	t.Helper()
	remaining := int64(qualificationAmountMillicents / 1000)
	if open.PaymentStatus != state.InvoicePaymentOpen || open.DueAt == nil || open.AmountRemainingMinor == nil || *open.AmountRemainingMinor != remaining {
		t.Fatalf("synced export = %+v", open)
	}
	if !strings.HasPrefix(open.HostedInvoiceURL, "https://") && p.hostedURL {
		t.Fatalf("%s gave no https page to pay invoice %s (hosted URL %q)", p.name, open.InvoiceID, open.HostedInvoiceURL)
	}
	var docs api.ProductBillingDocuments
	if err := json.Unmarshal(q.call(t, "GET", "/v1/apps/"+q.app+"/product/billing/documents?tenant_id="+q.tenant+"&period="+q.period, nil, http.StatusOK).Body.Bytes(), &docs); err != nil {
		t.Fatal(err)
	}
	if len(docs.Documents) != 1 || docs.Documents[0].Invoice == nil {
		t.Fatalf("owner statements = %+v", docs)
	}
	invoice := docs.Documents[0].Invoice
	if invoice.InvoiceID != open.InvoiceID || invoice.PaymentStatus != state.InvoicePaymentOpen || invoice.HostedInvoiceURL != api.ProviderHostedURL(open.HostedInvoiceURL) ||
		invoice.AmountRemainingMillicents == nil || *invoice.AmountRemainingMillicents != qualificationAmountMillicents {
		t.Fatalf("owner statement invoice = %+v", invoice)
	}
	q.evidence["due_at"], q.evidence["hosted_invoice_url"] = open.DueAt, open.HostedInvoiceURL
	q.pass(fmt.Sprintf("payment status synced: open, EUR %d.%02d remaining, due %s", remaining/100, remaining%100, open.DueAt.Format(time.DateOnly)))
}

// checkOverdueHold makes the invoice overdue by moving both workers past its
// real due date, then settles it in the provider and expects the hold to
// lift.
func (q *qualification) checkOverdueHold(t *testing.T, p qualifyProvider, invoiceID string, open api.StatementInvoiceExportResponse) {
	t.Helper()
	q.call(t, "PUT", "/v1/apps/"+q.app+"/product/billing/overdue-policy", map[string]int{"block_overdue_after_days": 0}, http.StatusOK)
	overdueAt := open.DueAt.Add(time.Hour)
	// The response's overdue flag reads the real clock; the hold below
	// reads the moved one.
	if overdue := q.syncedExport(t, overdueAt); overdue.PaymentStatus != state.InvoicePaymentOpen || overdue.StatusSyncedAt == nil || !overdue.StatusSyncedAt.Equal(overdueAt) {
		t.Fatalf("export after its due date = %+v", overdue)
	}
	holds := q.holds(t, overdueAt)
	if len(holds) != 1 || holds[0].TenantID != q.tenant || holds[0].DocumentID != q.doc.ID || holds[0].HostedInvoiceURL != api.ProviderHostedURL(open.HostedInvoiceURL) {
		t.Fatalf("holds after the due date = %+v", holds)
	}
	q.pass("overdue policy held the customer once the invoice was past due")

	want := p.settle(t, invoiceID)
	settledAt := overdueAt.Add(api.StatementInvoiceOverdueSyncInterval + time.Minute)
	if settled := q.syncedExport(t, settledAt); settled.PaymentStatus != want {
		t.Fatalf("export after settling in %s = %+v, want %s", p.name, settled, want)
	}
	if holds := q.holds(t, settledAt); len(holds) != 0 {
		t.Fatalf("holds after settling = %+v", holds)
	}
	q.evidence["settled_status"] = want
	q.pass("settled in the provider: payment status " + want + ", hold lifted")
}

func (q *qualification) pass(check string) { q.checks = append(q.checks, check) }

func (q *qualification) writeEvidence(t *testing.T) {
	t.Helper()
	q.evidence["checks"] = q.checks
	for _, check := range q.checks {
		t.Logf("%s: %s", q.provider, check)
	}
	dir := os.Getenv("FAAS_QUALIFY_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	raw, err := json.MarshalIndent(q.evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "product-control-"+q.provider+".json"), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// providerCall calls a provider's own API with the test key and decodes its
// JSON answer. The key is sent only in the Authorization header and never
// printed.
func providerCall(t *testing.T, method, target, contentType string, body io.Reader, key string, header map[string]string) map[string]any {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, req.URL.Path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		t.Fatalf("%s %s: %d %s", method, req.URL.Path, res.StatusCode, raw)
	}
	out := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s: %v", method, req.URL.Path, err)
		}
	}
	return out
}

func qualificationEmail(provider string) string {
	return fmt.Sprintf("qualification+%s-%s@example.com", provider, uuid.NewString()[:8])
}

func jsonBody(t *testing.T, v any) io.Reader {
	t.Helper()
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(raw)
}

func stripeQualification(key string) qualifyProvider {
	call := func(t *testing.T, method, path string, form url.Values) map[string]any {
		return providerCall(t, method, "https://api.stripe.com"+path, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), key, nil)
	}
	return qualifyProvider{
		name:       state.BillingProviderStripe,
		connection: api.PutBillingConnectionRequest{SecretKey: key, Collection: state.CollectionSend, DaysUntilDue: 14},
		customer: func(t *testing.T, label string) string {
			id, _ := call(t, "POST", "/v1/customers", url.Values{"name": {label}, "email": {qualificationEmail("stripe")}})["id"].(string)
			return id
		},
		settle: func(t *testing.T, invoiceID string) string {
			call(t, "POST", "/v1/invoices/"+url.PathEscape(invoiceID)+"/pay", url.Values{"paid_out_of_band": {"true"}})
			return state.InvoicePaymentPaid
		},
		hostedURL: true,
	}
}

func lagoQualification(key, base string) qualifyProvider {
	call := func(t *testing.T, method, path string, body any) map[string]any {
		return providerCall(t, method, base+"/api/v1"+path, "application/json", jsonBody(t, body), key, nil)
	}
	return qualifyProvider{
		name:       state.BillingProviderLago,
		connection: api.PutBillingConnectionRequest{SecretKey: key, APIURL: base},
		customer: func(t *testing.T, label string) string {
			id := "gregale-qualification-" + uuid.NewString()[:8]
			call(t, "POST", "/customers", map[string]any{"customer": map[string]any{"external_id": id, "name": label, "email": qualificationEmail("lago"),
				"currency": "EUR", "net_payment_term": 14}})
			return id
		},
		settle: func(t *testing.T, invoiceID string) string {
			call(t, "PUT", "/invoices/"+url.PathEscape(invoiceID), map[string]any{"invoice": map[string]any{"payment_status": "succeeded"}})
			return state.InvoicePaymentPaid
		},
	}
}

func paddleQualification(key string) qualifyProvider {
	call := func(t *testing.T, method, path string, body any) map[string]any {
		out := providerCall(t, method, paddleinvoice.SandboxURL+path, "application/json", jsonBody(t, body), key, map[string]string{"Paddle-Version": paddleinvoice.APIVersion})
		data, _ := out["data"].(map[string]any)
		return data
	}
	return qualifyProvider{
		name:       state.BillingProviderPaddle,
		connection: api.PutBillingConnectionRequest{SecretKey: key, Collection: state.CollectionSend, DaysUntilDue: 14},
		customer: func(t *testing.T, label string) string {
			id, _ := call(t, "POST", "/customers", map[string]any{"email": qualificationEmail("paddle"), "name": label})["id"].(string)
			return id
		},
		beforeExport: func(t *testing.T, q *qualification) { qualifyPaddleBillingDetails(t, q, key) },
		afterExport: func(t *testing.T, q *qualification, invoiceID string) {
			txn := call(t, "GET", "/transactions/"+url.PathEscape(invoiceID), nil)
			if txn["status"] != "billed" || txn["address_id"] != q.evidence["address_id"] || txn["business_id"] != q.evidence["business_id"] {
				t.Fatalf("Paddle transaction %s: status %v, address %v, business %v; want billed with %v and %v", invoiceID, txn["status"],
					txn["address_id"], txn["business_id"], q.evidence["address_id"], q.evidence["business_id"])
			}
			q.pass("Paddle billed the transaction with the newest address and the business")
		},
		// A Paddle sandbox invoice is paid only through checkout, so the
		// qualification cancels it, which also lifts the hold.
		settle: func(t *testing.T, invoiceID string) string {
			call(t, "PATCH", "/transactions/"+url.PathEscape(invoiceID), map[string]any{"status": "canceled"})
			return state.InvoicePaymentVoid
		},
		hostedURL: true,
	}
}

// qualifyPaddleBillingDetails checks ADR-20261019100333's missing-address
// refusal, then writes, reads and updates the customer's billing details as
// the portal does (ADR-20261020120000), and checks that a second address
// created in Paddle becomes the one invoices use (amendment 1).
func qualifyPaddleBillingDetails(t *testing.T, q *qualification, key string) {
	t.Helper()
	ctx := t.Context()
	w := q.call(t, "POST", q.invoicePath(), api.ProductInvoiceExportRequest{TenantID: q.tenant, Provider: q.provider}, http.StatusUnprocessableEntity)
	if !strings.Contains(w.Body.String(), "no address") {
		t.Fatalf("export without an address: %s", w.Body)
	}
	q.pass("export without an address was refused and kept the draft")

	request := api.PutProductBillingDetailsRequest{Address: api.ProductBillingAddress{CountryCode: "DE", PostalCode: "10115", City: "Berlin",
		FirstLine: "Invalidenstrasse 1", SecondLine: "Floor 2"}, Business: &api.ProductBillingBusiness{Name: "Qualification GmbH", TaxIdentifier: "DE123456789"}}
	if _, changed, problem := q.invoices.PutPaddleBillingDetails(ctx, q.account, q.customer, request); problem != nil || len(changed) != 2 {
		t.Fatalf("first billing details write: %v %+v", changed, problem)
	}
	read, problem := q.invoices.PaddleBillingDetails(ctx, q.account, q.customer)
	if problem != nil || read.Address == nil || *read.Address != request.Address || read.Business == nil || *read.Business != *request.Business {
		t.Fatalf("billing details read back = %+v %+v, want %+v", read, problem, request)
	}
	if _, changed, problem := q.invoices.PutPaddleBillingDetails(ctx, q.account, q.customer, request); problem != nil || len(changed) != 0 {
		t.Fatalf("unchanged billing details were written: %v %+v", changed, problem)
	}
	moved := request
	moved.Address.City, moved.Address.SecondLine = "Potsdam", ""
	if _, changed, problem := q.invoices.PutPaddleBillingDetails(ctx, q.account, q.customer, moved); problem != nil || len(changed) != 1 || changed[0] != "address" {
		t.Fatalf("address update: %v %+v", changed, problem)
	}
	if read, problem := q.invoices.PaddleBillingDetails(ctx, q.account, q.customer); problem != nil || read.Address == nil || *read.Address != moved.Address {
		t.Fatalf("updated address read back = %+v %+v", read.Address, problem)
	}
	q.pass("billing details written, read back field for field, unchanged writes skipped, a cleared line cleared")

	client := paddleinvoice.New()
	added, err := client.PutAddress(ctx, key, q.customer, paddleinvoice.Address{CountryCode: "DE", PostalCode: "80331", City: "Munich", FirstLine: "Marienplatz 1"})
	if err != nil {
		t.Fatal(err)
	}
	newest, ok, err := client.ActiveAddress(ctx, key, q.customer)
	if err != nil || !ok || newest.ID != added.ID {
		t.Fatalf("newest active address = %+v %v %v, want %s", newest, ok, err, added.ID)
	}
	business, ok, err := client.ActiveBusiness(ctx, key, q.customer)
	if err != nil || !ok {
		t.Fatalf("active business = %+v %v %v", business, ok, err)
	}
	q.evidence["address_id"], q.evidence["business_id"] = added.ID, business.ID
	q.pass("Paddle lists the newest created address first (order_by=id[DESC])")
}
