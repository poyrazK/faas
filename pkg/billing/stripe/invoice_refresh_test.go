package stripe

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
	stripeapi "github.com/stripe/stripe-go"
)

// adr:383
func TestInvoiceRefreshStripePaginationAndPrices(t *testing.T) {
	raw, err := os.ReadFile("../testdata/invoices/stripe.json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	var facts stripeInvoiceFacts
	if err := json.Unmarshal(envelope.Data.Object, &facts); err != nil {
		t.Fatal(err)
	}
	lines := facts.Lines.Data
	lines[0].Price = json.RawMessage(`"price_plan"`)
	lines[1].Price = nil
	lines[1].Pricing.PriceDetails.Price = json.RawMessage(`"price_usage"`)
	zero := int64(0)
	lines = append(lines, stripeInvoiceLine{ID: "il_zero", Currency: "eur", Amount: &zero, Price: json.RawMessage(`"price_plan"`)})
	pages, prices := 0, map[string]int{}
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, _, ok := r.BasicAuth()
		if r.Method != http.MethodGet || !ok || key != "test-key" || r.Header.Get("Stripe-Version") != stripeapi.APIVersion {
			t.Error("incorrect authenticated Stripe read")
		}
		switch r.URL.Path {
		case "/v1/invoices/in_invoice":
			body := envelope.Data.Object
			if mode == "foreign" {
				var obj map[string]json.RawMessage
				_ = json.Unmarshal(body, &obj)
				obj["customer"] = json.RawMessage(`"cus_foreign"`)
				body, _ = json.Marshal(obj)
			}
			_, _ = w.Write(body)
		case "/v1/invoices/in_invoice/lines":
			pages++
			if mode == "page failure" && r.URL.Query().Get("starting_after") != "" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if r.URL.Query().Get("limit") != "100" {
				t.Error("line page size missing")
			}
			more := true
			data := lines[:1]
			if r.URL.Query().Get("starting_after") != "" {
				if r.URL.Query().Get("starting_after") != lines[0].ID {
					t.Error("wrong page cursor")
				}
				more, data = false, lines[1:]
				if mode == "repeated cursor" {
					data = lines[:1]
					more = true
				}
			}
			_ = json.NewEncoder(w).Encode(stripeInvoiceLines{HasMore: &more, Data: data})
		case "/v1/prices/price_plan":
			prices["plan"]++
			_, _ = w.Write([]byte(`{"id":"price_plan","recurring":{"usage_type":"licensed"}}`))
		case "/v1/prices/price_usage":
			prices["usage"]++
			if mode == "price failure" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if mode == "price identity" {
				_, _ = w.Write([]byte(`{"id":"price_foreign"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"price_usage","recurring":{"usage_type":"metered"}}`))
		default:
			t.Errorf("unexpected URL: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := &Client{apiKey: "test-key", invoiceBaseURL: srv.URL, invoiceHTTPClient: srv.Client()}
	acct := state.Account{ID: "account", ProviderCustomerID: "cus_invoice"}
	inv := state.Invoice{AccountID: acct.ID, Provider: "stripe", ProviderInvoiceID: "in_invoice", TotalCents: 1190, TaxCents: 190, Currency: "eur"}
	details, err := c.FetchInvoiceDetails(t.Context(), acct, inv)
	if err != nil {
		t.Fatal(err)
	}
	inv.Details = details
	if state.InvoiceLineGap(inv) != "" || pages != 2 || prices["plan"] != 1 || prices["usage"] != 1 || details.Lines.Items[1].ChargeCategory != "Usage" {
		t.Fatalf("pagination/classification failed: %+v pages=%d prices=%v", details, pages, prices)
	}
	mode = "foreign"
	pages = 0
	if _, err := c.FetchInvoiceDetails(t.Context(), acct, inv); !errors.Is(err, billing.ErrInvoiceSourceMismatch) || pages != 0 {
		t.Fatal("foreign customer permitted line/price reads")
	}
	for _, failure := range []string{"page failure", "repeated cursor", "price failure", "price identity"} {
		mode = failure
		if _, err := c.FetchInvoiceDetails(t.Context(), acct, inv); err == nil {
			t.Fatalf("accepted %s", mode)
		}
	}
	// The SDK's pinned API version may expose legacy plans instead of prices.
	lines[0].Price, lines[0].Plan = json.RawMessage(`null`), json.RawMessage(`{"usage_type":"licensed"}`)
	mode = "ok"
	details, err = c.FetchInvoiceDetails(t.Context(), acct, inv)
	if err != nil || details.Lines.Items[0].ChargeCategory != "Purchase" {
		t.Fatalf("legacy plan category missing: %v", err)
	}
	inv.TotalCents++
	if _, err := c.FetchInvoiceDetails(t.Context(), acct, inv); !errors.Is(err, billing.ErrInvoiceSourceMismatch) {
		t.Fatal("remote amount mismatch accepted")
	}
}
