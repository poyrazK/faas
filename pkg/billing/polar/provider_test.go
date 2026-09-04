package polar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeUsageDedupe struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (d *fakeUsageDedupe) HasStripePushHour(_ context.Context, accountID string, hour time.Time) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seen[accountID+hour.UTC().Format(time.RFC3339)], nil
}

func (d *fakeUsageDedupe) RecordStripePushHour(_ context.Context, accountID string, hour time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen[accountID+hour.UTC().Format(time.RFC3339)] = true
	return nil
}

func testConfig(baseURL string) Config {
	return Config{
		APIKey:           "polar_test_token",
		WebhookSecret:    base64.StdEncoding.EncodeToString([]byte("webhook-secret")),
		BaseURL:          baseURL,
		HobbyProductID:   "hobby-product",
		ProProductID:     "pro-product",
		ScaleProductID:   "scale-product",
		UsageEventName:   "ram_usage",
		MeterID:          "meter-1",
		ToleranceSeconds: 300,
	}
}

func catalogProductJSON(id string, fixedCents int64) string {
	return `{"id":"` + id + `","recurring_interval":"month","recurring_interval_count":1,"is_recurring":true,"is_archived":false,"prices":[{"amount_type":"fixed","price_currency":"eur","price_amount":` + strconv.FormatInt(fixedCents, 10) + `,"is_archived":false},{"amount_type":"metered_unit","price_currency":"eur","unit_amount":"1","meter_id":"meter-1","cap_amount":null,"is_archived":false}],"benefits":[]}`
}

func catalogMeterJSON() string {
	return `{"id":"meter-1","unit":"scalar","archived_at":null,"filter":{"conjunction":"and","clauses":[{"property":"name","operator":"eq","value":"ram_usage"}]},"aggregation":{"func":"sum","property":"gb_ram_hours"}}`
}

func TestNewProviderRequiresAccessToken(t *testing.T) {
	_, err := NewProvider(Config{}, nil)
	if err == nil || !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("NewProvider error = %v, want ErrNoAPIKey", err)
	}
}

func TestEnsurePlanProductsRequiresConfiguredIDs(t *testing.T) {
	p, err := NewProvider(testConfig("http://example.test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	p.products[api.PlanPro] = ""
	if err := p.EnsurePlanProducts(context.Background()); err == nil || !strings.Contains(err.Error(), "pro") {
		t.Fatalf("EnsurePlanProducts error = %v, want missing pro product", err)
	}
}

func TestEnsurePlanProductsValidatesConfiguredCatalog(t *testing.T) {
	var productRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/v1/meters/meter-1" {
			_, _ = io.WriteString(w, catalogMeterJSON())
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/v1/products/") {
			http.NotFound(w, r)
			return
		}
		productRequests++
		id := strings.TrimPrefix(r.URL.Path, "/v1/products/")
		fixed := int64(900)
		switch id {
		case "pro-product":
			fixed = 2900
		case "scale-product":
			fixed = 9900
		}
		_, _ = io.WriteString(w, catalogProductJSON(id, fixed))
	}))
	defer server.Close()
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.EnsurePlanProducts(context.Background()); err != nil {
		t.Fatalf("EnsurePlanProducts: %v", err)
	}
	if productRequests != 3 {
		t.Fatalf("catalog product requests = %d, want 3", productRequests)
	}
	entries := p.ListBillingCatalog(context.Background())
	if len(entries) != 3 {
		t.Fatalf("Polar catalog entries = %d, want 3", len(entries))
	}
	for _, entry := range entries {
		if entry.Kind != api.BillingCatalogKindProduct || entry.Handle == "" {
			t.Errorf("catalog entry = %+v, want a product handle", entry)
		}
		if entry.SyncedAt.IsZero() {
			t.Errorf("catalog entry %s has zero synced_at", entry.Plan)
		}
	}
}

func TestEnsurePlanProductsRejectsWrongPriceBeforeStartup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/meters/meter-1" {
			_, _ = io.WriteString(w, catalogMeterJSON())
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/products/") {
			id := strings.TrimPrefix(r.URL.Path, "/v1/products/")
			_, _ = io.WriteString(w, catalogProductJSON(id, 1))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.EnsurePlanProducts(context.Background()); err == nil || !strings.Contains(err.Error(), "fixed price") {
		t.Fatalf("EnsurePlanProducts error = %v, want fixed-price validation", err)
	}
}

func TestEnsurePlanProductsRejectsPolarMeterCredits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/meters/meter-1" {
			_, _ = io.WriteString(w, catalogMeterJSON())
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/products/") {
			id := strings.TrimPrefix(r.URL.Path, "/v1/products/")
			fixed := int64(900)
			switch id {
			case "pro-product":
				fixed = 2900
			case "scale-product":
				fixed = 9900
			}
			product := strings.Replace(catalogProductJSON(id, fixed), `"benefits":[]`, `"benefits":[{"type":"meter_credit","is_deleted":false}]`, 1)
			_, _ = io.WriteString(w, product)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.EnsurePlanProducts(context.Background()); err == nil || !strings.Contains(err.Error(), "meter credits") {
		t.Fatalf("EnsurePlanProducts error = %v, want meter-credit validation", err)
	}
}

func TestEnsurePlanProductsRequiresMeterID(t *testing.T) {
	cfg := testConfig("http://example.test")
	cfg.MeterID = ""
	p, err := NewProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.EnsurePlanProducts(context.Background()); err == nil || !strings.Contains(err.Error(), "FAAS_POLAR_METER_ID") {
		t.Fatalf("EnsurePlanProducts error = %v, want meter-id validation", err)
	}
}

func TestVerifyWebhookAcceptsRawPolarSecret(t *testing.T) {
	cfg := testConfig("http://example.test")
	cfg.WebhookSecret = "polar_whs_test_secret"
	p, err := NewProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC()
	body := []byte(`{"type":"order.paid","data":{"id":"order-raw","customer_id":"customer-1","total_amount":100,"currency":"eur"}}`)
	headers := map[string]string{
		"webhook-id":        "raw-1",
		"webhook-timestamp": strconv.FormatInt(when.Unix(), 10),
		"webhook-signature": SignForTest(body, cfg.WebhookSecret, "raw-1", when),
	}
	ev, err := p.VerifyWebhook(body, headers, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Invoice == nil || ev.Invoice.ProviderInvoiceID != "order-raw" || ev.Invoice.Status != "paid" {
		t.Fatalf("invoice projection = %+v, want paid order", ev.Invoice)
	}
}

func TestVerifyWebhookProjectsPolarOrderInvoice(t *testing.T) {
	p, err := NewProvider(testConfig("http://example.test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC()
	body := []byte(`{"type":"order.created","data":{"id":"order-1","customer_id":"customer-1","invoice_number":"INV-1","status":"pending","paid":false,"subtotal_amount":900,"tax_amount":100,"total_amount":1000,"currency":"eur","current_period_start":"2026-08-01T00:00:00Z","current_period_end":"2026-09-01T00:00:00Z","is_invoice_generated":false}}`)
	headers := map[string]string{
		"webhook-id":        "invoice-1",
		"webhook-timestamp": strconv.FormatInt(when.Unix(), 10),
		"webhook-signature": SignForTest(body, "webhook-secret", "invoice-1", when),
	}
	ev, err := p.VerifyWebhook(body, headers, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != billing.EventUnknown {
		t.Fatalf("order.created type = %v, want unknown state transition", ev.Type)
	}
	inv := ev.Invoice
	if inv == nil || inv.ProviderInvoiceID != "order-1" || inv.Number != "INV-1" || inv.Status != "open" || inv.SubtotalCents != 900 || inv.TaxCents != 100 || inv.TotalCents != 1000 || inv.AmountPaidCents != 0 || inv.PDFAvailable {
		t.Fatalf("invoice projection = %+v", inv)
	}
	if !inv.PeriodStart.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) || !inv.PeriodEnd.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("invoice period = %v..%v", inv.PeriodStart, inv.PeriodEnd)
	}
}

func TestDoJSONRetriesTransientResponses(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]bool
	if err := p.doJSON(context.Background(), http.MethodPost, "/v1/retry", map[string]string{"x": "y"}, &out, "retry-key"); err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if calls != 3 || !out["ok"] {
		t.Fatalf("calls=%d out=%v, want three attempts and ok response", calls, out)
	}
}

func TestCreateCustomerAndCheckout(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/v1/customers/external/acct-1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path == "/v1/customers" {
			if r.Header.Get("Authorization") != "Bearer polar_test_token" {
				t.Error("missing Polar bearer token")
			}
			_, _ = io.WriteString(w, `{"id":"customer-1","external_id":"acct-1"}`)
			return
		}
		if r.URL.Path == "/v1/checkouts" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["external_customer_id"] != "acct-1" {
				t.Errorf("external_customer_id = %v", body["external_customer_id"])
			}
			products := body["products"].([]any)
			if len(products) != 1 || products[0] != "hobby-product" {
				t.Errorf("products = %v", products)
			}
			_, _ = io.WriteString(w, `{"id":"checkout-1","url":"https://checkout.polar.test/1"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	acct := state.Account{ID: "acct-1", Email: "dev@example.com"}
	customerID, err := p.CreateCustomer(context.Background(), acct)
	if err != nil || customerID != "customer-1" {
		t.Fatalf("CreateCustomer = %q, %v", customerID, err)
	}
	txID, checkoutURL, err := p.CreateUpgradeTransaction(context.Background(), state.Account{
		ID: "acct-1", Email: "dev@example.com", ProviderCustomerID: customerID,
	}, api.PlanHobby)
	if err != nil || txID != "checkout-1" || checkoutURL == "" {
		t.Fatalf("CreateUpgradeTransaction = %q, %q, %v", txID, checkoutURL, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 3 || methods[0] != "GET /v1/customers/external/acct-1" || methods[1] != "POST /v1/customers" || methods[2] != "POST /v1/checkouts" {
		t.Fatalf("request sequence = %v", methods)
	}
}

func TestCreateCustomerPortalSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/customer-sessions" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer polar_test_token" {
			t.Errorf("authorization = %q, want bearer token", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["external_customer_id"] != "acct-1" {
			t.Errorf("external_customer_id = %v, want acct-1", body["external_customer_id"])
		}
		if body["return_url"] != "https://app.example.com/billing" {
			t.Errorf("return_url = %v, want app URL", body["return_url"])
		}
		_, _ = io.WriteString(w, `{"customer_portal_url":"https://polar.test/portal/session-1"}`)
	}))
	defer server.Close()

	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.CreateCustomerPortalSession(context.Background(), state.Account{ID: "acct-1"}, "https://app.example.com/billing")
	if err != nil {
		t.Fatalf("CreateCustomerPortalSession: %v", err)
	}
	if got != "https://polar.test/portal/session-1" {
		t.Fatalf("portal URL = %q, want Polar session URL", got)
	}
}

func TestChangeSubscriptionPlanSchedulesNextPeriodUpdate(t *testing.T) {
	var gotIdempotency string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/subscriptions/sub-1" {
			http.NotFound(w, r)
			return
		}
		gotIdempotency = r.Header.Get("Idempotency-Key")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["product_id"] != "pro-product" || body["proration_behavior"] != "next_period" {
			t.Fatalf("subscription update body = %v", body)
		}
		_, _ = io.WriteString(w, `{"id":"sub-1","current_period_end":"2026-09-30T00:00:00Z","pending_update":{"applies_at":"2026-09-30T00:00:00Z"}}`)
	}))
	defer server.Close()

	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	effectiveAt, err := p.ChangeSubscriptionPlan(context.Background(), state.Account{
		ID: "acct-1", StripeSubscriptionItem: "sub-1",
	}, api.PlanPro)
	if err != nil {
		t.Fatalf("ChangeSubscriptionPlan: %v", err)
	}
	want := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	if !effectiveAt.Equal(want) {
		t.Fatalf("effective_at = %v, want %v", effectiveAt, want)
	}
	if gotIdempotency != "faas-plan-change-acct-1-pro" {
		t.Fatalf("idempotency key = %q", gotIdempotency)
	}
}

func TestReconcileUsageReadsMeterTotal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/meters/meter-1/quantities" {
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query()
		if query.Get("external_customer_id") != "acct-1" || query.Get("interval") != "hour" || query.Get("timezone") != "UTC" {
			t.Errorf("query = %v", query)
		}
		if query.Get("start_timestamp") == "" || query.Get("end_timestamp") == "" {
			t.Error("reconciliation time range is missing")
		}
		_, _ = io.WriteString(w, `{"quantities":[{"timestamp":"2026-08-31T10:00:00Z","quantity":1.5}],"total":1.5}`)
	}))
	defer server.Close()

	cfg := testConfig(server.URL)
	cfg.MeterID = "meter-1"
	p, err := NewProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	got, err := p.ReconcileUsage(context.Background(), state.Account{ID: "acct-1"}, start, end)
	if err != nil {
		t.Fatalf("ReconcileUsage: %v", err)
	}
	want := int64(1.5 * float64(billing.SecondsPerGBHour))
	if got != want {
		t.Errorf("ReconcileUsage = %d, want %d", got, want)
	}
	if !p.Capabilities().Has(billing.CapUsageReconcile) {
		t.Fatal("configured meter should advertise CapUsageReconcile")
	}
}

func TestReconcileUsageWithoutMeterIsUnsupported(t *testing.T) {
	cfg := testConfig("http://example.test")
	cfg.MeterID = ""
	p, err := NewProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.ReconcileUsage(context.Background(), state.Account{ID: "acct-1"}, time.Now().Add(-time.Hour), time.Now())
	if got != 0 || !errors.Is(err, billing.ErrNotImplemented) {
		t.Fatalf("ReconcileUsage = %d, %v, want 0/ErrNotImplemented", got, err)
	}
	if p.Capabilities().Has(billing.CapUsageReconcile) {
		t.Fatal("provider without meter should not advertise CapUsageReconcile")
	}
}

func TestPushUsageRecordUsesGBRamHoursAndDedupe(t *testing.T) {
	var calls int
	var received usageEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/events/ingest" {
			http.NotFound(w, r)
			return
		}
		calls++
		var body ingestRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		received = body.Events[0]
		_, _ = io.WriteString(w, `{"inserted":1,"duplicates":0}`)
	}))
	defer server.Close()
	dedupe := &fakeUsageDedupe{seen: map[string]bool{}}
	p, err := NewProviderWithDedupe(testConfig(server.URL), nil, dedupe)
	if err != nil {
		t.Fatal(err)
	}
	hour := time.Date(2026, 8, 31, 10, 37, 0, 0, time.FixedZone("TRT", 3*60*60))
	acct := state.Account{ID: "acct-1", ProviderCustomerID: "customer-1"}
	mbSeconds := int64(1024 * 3600)
	if err := p.PushUsageRecord(context.Background(), acct, hour, mbSeconds); err != nil {
		t.Fatal(err)
	}
	if err := p.PushUsageRecord(context.Background(), acct, hour.Add(12*time.Minute), mbSeconds); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("Polar ingest calls = %d, want 1 after hourly dedupe", calls)
	}
	if got := received.Metadata["gb_ram_hours"].(float64); got != 1 {
		t.Errorf("gb_ram_hours = %v, want 1", got)
	}
	if got := received.Metadata["mb_seconds"].(float64); got != float64(mbSeconds) {
		t.Errorf("mb_seconds = %v, want %d", got, mbSeconds)
	}
	if received.ExternalID != "faas-usage-acct-1-2026-08-31T07:00:00Z" {
		t.Errorf("external_id = %q, want stable hourly id", received.ExternalID)
	}
}

func TestRequestInvoicePDF(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RequestInvoicePDF(context.Background(), "order-1"); err != nil {
		t.Fatalf("RequestInvoicePDF: %v", err)
	}
	if gotPath != "POST /v1/orders/order-1/invoice" {
		t.Fatalf("request path = %q", gotPath)
	}
}

func TestVerifyWebhookNormalizesSubscriptionAndScheduledCancel(t *testing.T) {
	p, err := NewProvider(testConfig("http://example.test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC()
	body := []byte(`{"type":"subscription.updated","data":{"id":"sub-1","customer_id":"customer-1","product_id":"hobby-product","status":"active"}}`)
	id := "msg-1"
	headers := map[string]string{
		"Webhook-Id":        id,
		"Webhook-Timestamp": strconv.FormatInt(when.Unix(), 10),
		"Webhook-Signature": SignForTest(body, "webhook-secret", id, when),
	}
	ev, err := p.VerifyWebhook(body, headers, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != billing.EventSubscriptionUpdated || ev.CustomerID != "customer-1" || ev.SubscriptionID != "sub-1" || ev.PlanID != string(api.PlanHobby) {
		t.Fatalf("normalized event = %+v", ev)
	}

	cancelBody := []byte(`{"type":"subscription.canceled","data":{"id":"sub-1","customer_id":"customer-1","product_id":"hobby-product","status":"active","cancel_at_period_end":true}}`)
	cancelHeaders := map[string]string{
		"webhook-id":        "msg-2",
		"webhook-timestamp": strconv.FormatInt(when.Unix(), 10),
		"webhook-signature": SignForTest(cancelBody, "webhook-secret", "msg-2", when),
	}
	cancelEvent, err := p.VerifyWebhook(cancelBody, cancelHeaders, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if cancelEvent.Type != billing.EventSubscriptionUpdated {
		t.Fatalf("scheduled cancellation mapped to %v, want subscription_updated", cancelEvent.Type)
	}
}

func TestVerifyWebhookDoesNotActivateIncompleteSubscription(t *testing.T) {
	p, err := NewProvider(testConfig("http://example.test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC()
	body := []byte(`{"type":"subscription.created","data":{"id":"sub-pending","customer_id":"customer-1","product_id":"hobby-product","status":"incomplete"}}`)
	headers := map[string]string{
		"webhook-id":        "pending-1",
		"webhook-timestamp": strconv.FormatInt(when.Unix(), 10),
		"webhook-signature": SignForTest(body, "webhook-secret", "pending-1", when),
	}
	ev, err := p.VerifyWebhook(body, headers, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != billing.EventUnknown {
		t.Fatalf("incomplete subscription event = %v, want unknown", ev.Type)
	}
	if ev.PlanID != string(api.PlanHobby) || ev.SubscriptionID != "sub-pending" {
		t.Fatalf("incomplete event lost provider identifiers: %+v", ev)
	}
}

func TestVerifyWebhookActivatesActiveCreatedSubscription(t *testing.T) {
	p, err := NewProvider(testConfig("http://example.test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC()
	body := []byte(`{"type":"subscription.created","data":{"id":"sub-active","customer_id":"customer-1","product_id":"hobby-product","status":"active"}}`)
	headers := map[string]string{
		"webhook-id":        "active-1",
		"webhook-timestamp": strconv.FormatInt(when.Unix(), 10),
		"webhook-signature": SignForTest(body, "webhook-secret", "active-1", when),
	}
	ev, err := p.VerifyWebhook(body, headers, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != billing.EventPaymentSucceeded {
		t.Fatalf("active subscription event = %v, want payment_succeeded", ev.Type)
	}
}

func TestVerifyWebhookRejectsTampering(t *testing.T) {
	p, err := NewProvider(testConfig("http://example.test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC()
	body := []byte(`{"type":"order.paid","data":{"id":"order-1","customer_id":"customer-1"}}`)
	headers := map[string]string{
		"webhook-id":        "msg-1",
		"webhook-timestamp": strconv.FormatInt(when.Unix(), 10),
		"webhook-signature": SignForTest(body, "wrong-secret", "msg-1", when),
	}
	if _, err := p.VerifyWebhook(body, headers, 5*time.Minute); err == nil || !errors.Is(err, billing.ErrBadSignature) {
		t.Fatalf("VerifyWebhook error = %v, want ErrBadSignature", err)
	}
}

func TestCancelAndRefund(t *testing.T) {
	var paths []string
	var refundIdempotencyKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/v1/subscriptions/sub-1" {
			_, _ = io.WriteString(w, `{"id":"sub-1","current_period_end":"2026-09-30T00:00:00Z","cancel_at_period_end":true}`)
			return
		}
		if r.URL.Path == "/v1/refunds" {
			refundIdempotencyKey = r.Header.Get("Idempotency-Key")
			_, _ = io.WriteString(w, `{"id":"refund-1","amount":500,"currency":"eur"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := p.CancelAtPeriodEnd(context.Background(), state.Account{ID: "acct-1", StripeSubscriptionItem: "sub-1"})
	if err != nil || effective.IsZero() {
		t.Fatalf("CancelAtPeriodEnd = %v, %v", effective, err)
	}
	refund, err := p.Refund(context.Background(), "order-1", 500)
	if err != nil || refund.ProviderRefundID != "refund-1" || refund.ChargeID != "order-1" {
		t.Fatalf("Refund = %+v, %v", refund, err)
	}
	if refundIdempotencyKey != "faas-refund-order-1-500" {
		t.Fatalf("refund idempotency key = %q", refundIdempotencyKey)
	}
	if len(paths) != 2 {
		t.Fatalf("API paths = %v", paths)
	}
}

func TestRefundUsesContextIdempotencyKey(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/refunds" {
			http.NotFound(w, r)
			return
		}
		got = r.Header.Get("Idempotency-Key")
		_, _ = io.WriteString(w, `{"id":"refund-operator","amount":250,"currency":"eur","status":"pending"}`)
	}))
	defer server.Close()
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := billing.ContextWithIdempotencyKey(context.Background(), "operator-refund-42")
	if _, err := p.Refund(ctx, "order-1", 250); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if got != "operator-refund-42" {
		t.Fatalf("refund idempotency key = %q, want operator-refund-42", got)
	}
}
