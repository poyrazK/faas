//go:build monetization_sandbox

package apidserver_test

// API monetization against the real provider sandboxes
// (ADR-20261020170649): `make monetization-sandbox`. Each provider runs only
// when its keys are set, in the environment or in the file
// GREGALE_SANDBOX_ENV_FILE names (default
// ~/.config/gregale/monetization-sandbox.env, KEY=value lines):
//
//	GREGALE_SANDBOX_STRIPE_KEY   sk_test_… or rk_test_… (customers, payment methods, invoices, invoice items)
//	GREGALE_SANDBOX_PADDLE_KEY   pdl_sdbx_apikey_… (customers, addresses, transactions)
//	GREGALE_SANDBOX_LAGO_KEY     a key of a Lago organization kept for testing; Lago has no test mode
//	GREGALE_SANDBOX_LAGO_URL     its API origin, e.g. https://api.getlago.com
//
// Live Stripe and Paddle keys are refused. The test creates its own
// customers and voids, cancels or deletes what it created.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

func sandboxEnv(t *testing.T) func(string) string {
	t.Helper()
	values := map[string]string{}
	path := os.Getenv("GREGALE_SANDBOX_ENV_FILE")
	if path == "" {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, ".config", "gregale", "monetization-sandbox.env")
		}
	}
	if f, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if key, value, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
				values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
			}
		}
		_ = f.Close()
	}
	return func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return values[key]
	}
}

// sandboxHTTP calls a provider's REST API directly, for fixtures and checks
// apid does not make.
type sandboxHTTP struct {
	t      *testing.T
	base   string
	header http.Header
	form   bool
}

func (c sandboxHTTP) call(method, path string, body any, out any) int {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		if c.form {
			reader = strings.NewReader(body.(url.Values).Encode())
		} else {
			raw, err := json.Marshal(body)
			if err != nil {
				c.t.Fatal(err)
			}
			reader = strings.NewReader(string(raw))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header = c.header.Clone()
	if body != nil && c.form {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if out != nil && res.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: decode %s: %v", method, path, raw, err)
		}
	}
	if res.StatusCode >= 300 {
		c.t.Logf("%s %s: %d %s", method, path, res.StatusCode, raw)
	}
	return res.StatusCode
}

// sandboxFixture is an apid with API monetization on and one priced app
// per currency.
type sandboxFixture struct {
	t     *testing.T
	e     apidserver.TestEnv
	month time.Time
	apps  map[string]string // currency -> app slug
}

func newSandboxFixture(t *testing.T) *sandboxFixture {
	t.Helper()
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanPro)
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	return &sandboxFixture{t: t, e: e, month: month, apps: map[string]string{}}
}

func sandboxJSON[T any](f *sandboxFixture, method, path string, body any, want int) T {
	f.t.Helper()
	res := f.e.Do(f.t, method, path, body, nil)
	var out T
	if res.Code != want || (res.Body.Len() > 0 && json.Unmarshal(res.Body.Bytes(), &out) != nil) {
		f.t.Fatalf("%s %s: %d %s, want %d", method, path, res.Code, res.Body, want)
	}
	return out
}

// app is the slug of an app whose rate card prices one request at price
// millicents in currency since the start of last month.
func (f *sandboxFixture) app(currency string, price int64) string {
	f.t.Helper()
	if slug, ok := f.apps[currency]; ok {
		return slug
	}
	slug := "sandbox-" + strings.ToLower(currency)
	apidserver.MustSeedApp(f.t, f.e, slug)
	sandboxJSON[api.APIConsumerRateCardResponse](f, http.MethodPost, "/v1/apps/"+slug+"/rate-cards",
		api.CreateAPIConsumerRateCardRequest{Currency: currency, PriceMillicentsPerUnit: price, EffectiveFrom: &f.month}, http.StatusCreated)
	f.apps[currency] = slug
	return slug
}

// statement creates a consumer billed to customer with units of last
// month's usage and credit, and returns its finalized statement's invoice
// path.
func (f *sandboxFixture) statement(slug, customer, currency string, units, creditMillicents int64) string {
	f.t.Helper()
	consumer := sandboxJSON[api.APIConsumerResponse](f, http.MethodPost, "/v1/apps/"+slug+"/consumers",
		api.CreateAPIConsumerRequest{ExternalRef: customer, Name: "Sandbox " + customer}, http.StatusCreated)
	if _, err := (*f.e.Store()).RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{EventID: uuid.NewString(),
		AccountID: (*f.e.Acct()).ID, AppID: consumer.AppID, ConsumerKey: consumer.ID, WindowStart: f.month.Add(time.Hour),
		RequestCount: units, BillableUnits: units}); err != nil {
		f.t.Fatal(err)
	}
	base := "/v1/apps/" + slug + "/consumers/" + consumer.ID
	if creditMillicents > 0 {
		sandboxJSON[api.APIConsumerCreditResponse](f, http.MethodPost, base+"/credits",
			api.CreateAPIConsumerCreditRequest{Currency: currency, AmountMillicents: creditMillicents}, http.StatusCreated)
	}
	end := f.month.AddDate(0, 1, 0)
	draft := sandboxJSON[api.APIConsumerUsageStatementResponse](f, http.MethodPost, base+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &f.month, PeriodEnd: &end}, http.StatusCreated)
	sandboxJSON[api.APIConsumerUsageStatementResponse](f, http.MethodPost, base+"/usage-statements/"+draft.ID+"/finalize", struct{}{}, http.StatusOK)
	return base + "/usage-statements/" + draft.ID + "/invoice"
}

func (f *sandboxFixture) connect(provider string, req api.PutBillingConnectionRequest) {
	f.t.Helper()
	sandboxJSON[api.BillingConnectionResponse](f, http.MethodPut, "/v1/account/billing-connections/"+provider, req, http.StatusOK)
}

func (f *sandboxFixture) export(path, provider, customer string, want int) api.StatementInvoiceExportResponse {
	f.t.Helper()
	return sandboxJSON[api.StatementInvoiceExportResponse](f, http.MethodPost, path,
		api.CreateStatementInvoiceRequest{Provider: provider, Customer: customer}, want)
}

// sync runs the payment sync as if its first delay had passed and returns
// the export as apid then reports it.
func (f *sandboxFixture) sync(path string) api.StatementInvoiceExportResponse {
	f.t.Helper()
	service := (*f.e.S()).StatementInvoices()
	service.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if err := service.SyncOnce(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	return sandboxJSON[api.StatementInvoiceExportResponse](f, http.MethodGet, path, nil, http.StatusOK)
}

// concurrentExports exports one statement from several requests at once:
// every success must name the same invoice and every refusal must be the
// in-progress 409 (ADR-20261019180000).
func (f *sandboxFixture) concurrentExports(path, provider string) string {
	f.t.Helper()
	var wg sync.WaitGroup
	results := make([]*httpResult, 4)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := f.e.Do(f.t, http.MethodPost, path, api.CreateStatementInvoiceRequest{Provider: provider}, nil)
			results[i] = &httpResult{code: res.Code, body: res.Body.String()}
		}(i)
	}
	wg.Wait()
	invoice := ""
	for _, r := range results {
		switch r.code {
		case http.StatusOK:
			var out api.StatementInvoiceExportResponse
			if err := json.Unmarshal([]byte(r.body), &out); err != nil || (invoice != "" && out.InvoiceID != invoice) {
				f.t.Fatalf("concurrent exports created different invoices: %s vs %s", invoice, r.body)
			}
			invoice = out.InvoiceID
		case http.StatusConflict:
			if !strings.Contains(r.body, api.CodeStatementInvoiceExportInProgress) {
				f.t.Fatalf("concurrent export refused with %s", r.body)
			}
		default:
			f.t.Fatalf("concurrent export: %d %s", r.code, r.body)
		}
	}
	if invoice == "" {
		invoice = f.export(path, provider, "", http.StatusOK).InvoiceID
	}
	return invoice
}

type httpResult struct {
	code int
	body string
}

// TestMonetizationSandboxStripe: draft with a credit line, a retry after a
// refused customer, concurrent exports, send, charge with payment sync, and
// JPY and KWD invoices in Stripe's units.
func TestMonetizationSandboxStripe(t *testing.T) {
	env := sandboxEnv(t)
	key := env("GREGALE_SANDBOX_STRIPE_KEY")
	if key == "" {
		t.Skip("GREGALE_SANDBOX_STRIPE_KEY is not set")
	}
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "rk_test_") {
		t.Fatal("GREGALE_SANDBOX_STRIPE_KEY must be a test-mode key (sk_test_ or rk_test_)")
	}
	stripe := sandboxHTTP{t: t, base: "https://api.stripe.com", form: true, header: http.Header{"Authorization": {"Bearer " + key}}}
	client := stripeinvoice.New()
	var invoices []string
	customer := func(withCard bool) string {
		t.Helper()
		var out struct {
			ID string `json:"id"`
		}
		if code := stripe.call(http.MethodPost, "/v1/customers", url.Values{"email": {"sandbox+" + uuid.NewString()[:8] + "@gregale.dev"},
			"name": {"Gregale sandbox"}, "metadata[gregale_sandbox]": {"1"}}, &out); code != http.StatusOK {
			t.Fatalf("create Stripe customer: %d", code)
		}
		t.Cleanup(func() { stripe.call(http.MethodDelete, "/v1/customers/"+out.ID, nil, nil) })
		if withCard {
			var pm struct {
				ID string `json:"id"`
			}
			if code := stripe.call(http.MethodPost, "/v1/payment_methods/pm_card_visa/attach", url.Values{"customer": {out.ID}}, &pm); code != http.StatusOK {
				t.Fatalf("attach test card: %d", code)
			}
			if code := stripe.call(http.MethodPost, "/v1/customers/"+out.ID, url.Values{"invoice_settings[default_payment_method]": {pm.ID}}, nil); code != http.StatusOK {
				t.Fatalf("default payment method: %d", code)
			}
		}
		return out.ID
	}
	t.Cleanup(func() {
		for _, id := range invoices {
			if code := stripe.call(http.MethodDelete, "/v1/invoices/"+id, nil, nil); code != http.StatusOK {
				stripe.call(http.MethodPost, "/v1/invoices/"+id+"/void", url.Values{}, nil)
			}
		}
	})
	f := newSandboxFixture(t)
	f.connect("stripe", api.PutBillingConnectionRequest{SecretKey: key})
	eur := f.app("EUR", 1250)

	// Draft: 300 requests at EUR 0.0125 is EUR 3.75; EUR 1.00 of credit leaves 2.75.
	draftCustomer := customer(false)
	draftPath := f.statement(eur, draftCustomer, "EUR", 300, 100000)
	draft := f.export(draftPath, "stripe", "", http.StatusOK)
	invoices = append(invoices, draft.InvoiceID)
	inv, err := client.GetInvoice(context.Background(), key, draft.InvoiceID)
	if err != nil || inv.Status != "draft" || inv.AmountRemaining != 275 || draft.AmountMinor != 275 {
		t.Fatalf("Stripe draft = %+v (%v), export = %+v", inv, err, draft)
	}
	if again := f.export(draftPath, "stripe", "", http.StatusOK); again.InvoiceID != draft.InvoiceID {
		t.Fatalf("repeated export made %s, want %s", again.InvoiceID, draft.InvoiceID)
	}

	// A customer Stripe does not know is refused; the right one is a new attempt.
	retryCustomer := customer(false)
	retryPath := f.statement(eur, "cus_gregaleSandboxMissing", "EUR", 100, 0)
	f.export(retryPath, "stripe", "", http.StatusUnprocessableEntity)
	retried := f.export(retryPath, "stripe", retryCustomer, http.StatusOK)
	invoices = append(invoices, retried.InvoiceID)

	// Concurrent exports of one statement make one invoice.
	invoices = append(invoices, f.concurrentExports(f.statement(eur, customer(false), "EUR", 100, 0), "stripe"))

	// Send: Stripe finalizes and emails it; the sync reports it open.
	f.connect("stripe", api.PutBillingConnectionRequest{SecretKey: key, Collection: "send", DaysUntilDue: 7})
	sentPath := f.statement(eur, customer(false), "EUR", 200, 0)
	sent := f.export(sentPath, "stripe", "", http.StatusOK)
	invoices = append(invoices, sent.InvoiceID)
	if synced := f.sync(sentPath); synced.PaymentStatus != state.InvoicePaymentOpen || synced.AmountRemainingMinor == nil || *synced.AmountRemainingMinor != 250 {
		t.Fatalf("sent invoice after sync = %+v", synced)
	}

	// Charge: Stripe charges the test card; the sync reports it paid.
	f.connect("stripe", api.PutBillingConnectionRequest{SecretKey: key, Collection: "charge"})
	chargedPath := f.statement(eur, customer(true), "EUR", 400, 0)
	charged := f.export(chargedPath, "stripe", "", http.StatusOK)
	invoices = append(invoices, charged.InvoiceID)
	var raw struct {
		Status           string `json:"status"`
		CollectionMethod string `json:"collection_method"`
	}
	stripe.call(http.MethodGet, "/v1/invoices/"+charged.InvoiceID, nil, &raw)
	if raw.CollectionMethod != "charge_automatically" {
		t.Fatalf("charged invoice collection = %+v", raw)
	}
	if raw.Status == "open" {
		stripe.call(http.MethodPost, "/v1/invoices/"+charged.InvoiceID+"/pay", url.Values{}, nil) // Stripe's own attempt can lag
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		inv, err := client.GetInvoice(context.Background(), key, charged.InvoiceID)
		if err == nil && inv.Status == "paid" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("charged invoice never paid: %+v %v", inv, err)
		}
		time.Sleep(3 * time.Second)
	}
	if synced := f.sync(chargedPath); synced.PaymentStatus != state.InvoicePaymentPaid {
		t.Fatalf("charged invoice after sync = %+v", synced)
	}

	// Zero- and three-decimal currencies (ADR-20261019100330): ¥375, and
	// KWD 4.1625 that Stripe writes to the nearest 10 fils.
	f.connect("stripe", api.PutBillingConnectionRequest{SecretKey: key})
	jpy := f.export(f.statement(f.app("JPY", 125000), customer(false), "JPY", 300, 0), "stripe", "", http.StatusOK)
	invoices = append(invoices, jpy.InvoiceID)
	if inv, err := client.GetInvoice(context.Background(), key, jpy.InvoiceID); err != nil || inv.AmountRemaining != 375 || jpy.AmountMinor != 375 {
		t.Fatalf("JPY invoice = %+v (%v), export %+v", inv, err, jpy)
	}
	kwd := f.export(f.statement(f.app("KWD", 1250), customer(false), "KWD", 333, 0), "stripe", "", http.StatusOK)
	invoices = append(invoices, kwd.InvoiceID)
	if inv, err := client.GetInvoice(context.Background(), key, kwd.InvoiceID); err != nil || inv.AmountRemaining != 4160 || kwd.AmountMinor != 4163 {
		t.Fatalf("KWD invoice = %+v (%v), export %+v", inv, err, kwd)
	}
}

// TestMonetizationSandboxPaddle: a draft transaction, concurrent exports,
// and send that waits for a customer address.
func TestMonetizationSandboxPaddle(t *testing.T) {
	env := sandboxEnv(t)
	key := env("GREGALE_SANDBOX_PADDLE_KEY")
	if key == "" {
		t.Skip("GREGALE_SANDBOX_PADDLE_KEY is not set")
	}
	if !strings.HasPrefix(key, "pdl_sdbx_") {
		t.Fatal("GREGALE_SANDBOX_PADDLE_KEY must be a sandbox key (pdl_sdbx_)")
	}
	paddle := sandboxHTTP{t: t, base: "https://sandbox-api.paddle.com",
		header: http.Header{"Authorization": {"Bearer " + key}, "Paddle-Version": {"1"}}}
	var transactions []string
	t.Cleanup(func() {
		for _, id := range transactions {
			paddle.call(http.MethodPatch, "/transactions/"+id, map[string]any{"status": "canceled"}, nil)
		}
	})
	customer := func() string {
		t.Helper()
		var out struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if code := paddle.call(http.MethodPost, "/customers", map[string]any{"email": "sandbox+" + uuid.NewString()[:8] + "@gregale.dev",
			"name": "Gregale sandbox"}, &out); code != http.StatusCreated && code != http.StatusOK {
			t.Fatalf("create Paddle customer: %d", code)
		}
		return out.Data.ID
	}
	address := func(id string) {
		t.Helper()
		if code := paddle.call(http.MethodPost, "/customers/"+id+"/addresses",
			map[string]any{"country_code": "US", "postal_code": "10001"}, nil); code != http.StatusCreated && code != http.StatusOK {
			t.Fatalf("add Paddle address: %d", code)
		}
	}
	transaction := func(id string) (status string, total string) {
		var out struct {
			Data struct {
				Status  string `json:"status"`
				Details struct {
					Totals struct {
						GrandTotal string `json:"grand_total"`
					} `json:"totals"`
				} `json:"details"`
			} `json:"data"`
		}
		paddle.call(http.MethodGet, "/transactions/"+id, nil, &out)
		return out.Data.Status, out.Data.Details.Totals.GrandTotal
	}
	f := newSandboxFixture(t)
	f.connect("paddle", api.PutBillingConnectionRequest{SecretKey: key})
	eur := f.app("EUR", 1250)

	// A draft: EUR 3.75 less EUR 1.00 of credit, as one summary item.
	drafted := customer()
	draft := f.export(f.statement(eur, drafted, "EUR", 300, 100000), "paddle", "", http.StatusOK)
	transactions = append(transactions, draft.InvoiceID)
	if status, total := transaction(draft.InvoiceID); status != "draft" || total != "275" {
		t.Fatalf("Paddle draft = %s %s", status, total)
	}
	transactions = append(transactions, f.concurrentExports(f.statement(eur, customer(), "EUR", 100, 0), "paddle"))

	// Send needs an address: refused without one, billed once it exists.
	f.connect("paddle", api.PutBillingConnectionRequest{SecretKey: key, Collection: "send", DaysUntilDue: 7})
	sentCustomer := customer()
	sentPath := f.statement(eur, sentCustomer, "EUR", 200, 0)
	f.export(sentPath, "paddle", "", http.StatusUnprocessableEntity)
	address(sentCustomer)
	sent := f.export(sentPath, "paddle", "", http.StatusOK)
	transactions = append(transactions, sent.InvoiceID)
	if status, _ := transaction(sent.InvoiceID); status != "billed" {
		t.Fatalf("sent Paddle transaction status = %s", status)
	}
	if synced := f.sync(sentPath); synced.PaymentStatus != state.InvoicePaymentOpen {
		t.Fatalf("sent transaction after sync = %+v", synced)
	}
}

// TestMonetizationSandboxLago: an issued one-off invoice and its payment
// status. Lago has no test mode, so it needs an organization kept for this.
func TestMonetizationSandboxLago(t *testing.T) {
	env := sandboxEnv(t)
	key, base := env("GREGALE_SANDBOX_LAGO_KEY"), strings.TrimRight(env("GREGALE_SANDBOX_LAGO_URL"), "/")
	if key == "" || base == "" {
		t.Skip("GREGALE_SANDBOX_LAGO_KEY and GREGALE_SANDBOX_LAGO_URL are not set")
	}
	lago := sandboxHTTP{t: t, base: base + "/api/v1", header: http.Header{"Authorization": {"Bearer " + key}}}
	externalID := "gregale-sandbox-" + uuid.NewString()[:8]
	if code := lago.call(http.MethodPost, "/customers", map[string]any{"customer": map[string]any{"external_id": externalID,
		"name": "Gregale sandbox", "currency": "EUR"}}, nil); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create Lago customer: %d", code)
	}
	f := newSandboxFixture(t)
	f.connect("lago", api.PutBillingConnectionRequest{SecretKey: key, APIURL: base})
	path := f.statement(f.app("EUR", 1250), externalID, "EUR", 300, 0)
	issued := f.export(path, "lago", "", http.StatusOK)
	t.Cleanup(func() { lago.call(http.MethodPost, "/invoices/"+issued.InvoiceID+"/void", map[string]any{}, nil) })
	if synced := f.sync(path); synced.PaymentStatus != state.InvoicePaymentOpen || issued.AmountMinor != 375 {
		t.Fatalf("Lago invoice = %+v, after sync %+v", issued, synced)
	}
	t.Logf("Lago invoice %s for %s", issued.InvoiceID, externalID)
}
