package paddleinvoice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// adr: 1507 — transactions are manual, address-less drafts with
// custom_data; retries find them; billed transactions report a due date.
func TestPaddleTransactions(t *testing.T) {
	var created map[string]any
	var headers http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/transactions":
			_ = json.NewDecoder(r.Body).Decode(&created)
			_, _ = fmt.Fprint(w, `{"data":{"id":"txn_1","status":"draft"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/transactions" && r.URL.Query().Get("customer_id") == "ctm_1":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"txn_0","custom_data":{"gregale_export_id":"other"}},{"id":"txn_1","custom_data":{"gregale_export_id":"exp-1"}}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/transactions/txn_1":
			_, _ = fmt.Fprint(w, `{"data":{"id":"txn_1","status":"past_due","currency_code":"EUR","billed_at":"2026-09-01T00:00:00Z",
				"details":{"totals":{"grand_total":"375","balance":"375"}},"billing_details":{"payment_terms":{"interval":"day","frequency":30}},
				"checkout":{"url":"https://pay.example.com/txn_1"},"payments":[]}}`)
		case r.URL.Path == "/transactions" && r.URL.Query().Get("per_page") == "1":
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `{"error":{"type":"request_error","code":"forbidden","detail":"missing permission"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"error":{"type":"request_error","code":"not_found","detail":"no such entity"}}`)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()
	key := "pdl_sdbx_apikey_abc"

	id, err := c.CreateTransaction(ctx, key, TransactionParams{CustomerID: "ctm_1", Currency: "eur",
		Items: []Item{{Description: "3 usage units", AmountMinor: 375}}, CustomData: map[string]string{"gregale_export_id": "exp-1"}})
	if err != nil || id != "txn_1" {
		t.Fatalf("CreateTransaction = %q, %v", id, err)
	}
	if headers.Get("Paddle-Version") != "1" || headers.Get("Authorization") != "Bearer "+key {
		t.Fatalf("headers = %v", headers)
	}
	if created["collection_mode"] != "manual" || created["currency_code"] != "EUR" || created["address_id"] != nil {
		t.Fatalf("body = %v", created)
	}
	item := created["items"].([]any)[0].(map[string]any)["price"].(map[string]any)
	if item["unit_price"].(map[string]any)["amount"] != "375" {
		t.Fatalf("item = %v", item)
	}
	if _, err := c.CreateTransaction(ctx, key, TransactionParams{CustomerID: "ctm_1", Currency: "EUR", Items: []Item{{Description: "credit", AmountMinor: -1}}}); err == nil {
		t.Fatal("negative item accepted")
	}
	if found, err := c.FindTransaction(ctx, key, "ctm_1", "exp-1"); err != nil || found != "txn_1" {
		t.Fatalf("FindTransaction = %q, %v", found, err)
	}
	st, err := c.GetTransaction(ctx, key, "txn_1")
	if err != nil || st.Status != "past_due" || st.GrandTotal != 375 || st.Balance != 375 || st.DueAt == nil ||
		st.DueAt.Format("2006-01-02") != "2026-10-01" || st.CheckoutURL == "" {
		t.Fatalf("GetTransaction = %+v, %v", st, err)
	}
	if gone, err := c.GetTransaction(ctx, key, "txn_gone"); err != nil || !gone.Deleted {
		t.Fatalf("missing transaction = %+v, %v", gone, err)
	}
	if err := c.CheckKey(ctx, key); !errors.Is(err, ErrKeyRejected) {
		t.Fatalf("CheckKey = %v, want ErrKeyRejected", err)
	}
	for k, live := range map[string]bool{"pdl_live_apikey_x": true, "pdl_sdbx_apikey_x": false} {
		if got, err := KeyLivemode(k); err != nil || got != live {
			t.Fatalf("KeyLivemode(%s) = %v, %v", k, got, err)
		}
	}
	if _, err := KeyLivemode("sk_live_x"); !errors.Is(err, ErrKeyFormat) {
		t.Fatal("Stripe key accepted as Paddle key")
	}
}

// adr: 20261019100333 — sending bills the transaction with the customer's
// newest active address; a customer without one is reported, not billed.
func TestPaddleBillTransaction(t *testing.T) {
	var billed map[string]any
	var terms map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/transactions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			terms = body["billing_details"].(map[string]any)["payment_terms"].(map[string]any)
			_, _ = fmt.Fprint(w, `{"data":{"id":"txn_1","status":"draft"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/customers/ctm_1/addresses":
			if r.URL.Query().Get("status") != "active" {
				t.Errorf("address query = %v", r.URL.Query())
			}
			_, _ = fmt.Fprint(w, `{"data":[{"id":"add_1"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/customers/ctm_2/addresses":
			_, _ = fmt.Fprint(w, `{"data":[]}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/transactions/txn_1":
			_ = json.NewDecoder(r.Body).Decode(&billed)
			_, _ = fmt.Fprint(w, `{"data":{"id":"txn_1","status":"billed"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"error":{"type":"request_error","code":"not_found","detail":"no such entity"}}`)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()
	key := "pdl_sdbx_apikey_abc"
	if _, err := c.CreateTransaction(ctx, key, TransactionParams{CustomerID: "ctm_1", Currency: "EUR", PaymentTermsDays: 14,
		Items: []Item{{Description: "usage", AmountMinor: 100}}}); err != nil || terms["frequency"] != float64(14) {
		t.Fatalf("payment terms = %v, %v", terms, err)
	}
	address, err := c.CustomerAddress(ctx, key, "ctm_1")
	if err != nil || address != "add_1" {
		t.Fatalf("CustomerAddress = %q, %v", address, err)
	}
	if err := c.BillTransaction(ctx, key, "txn_1", address, ""); err != nil || billed["status"] != "billed" || billed["address_id"] != "add_1" {
		t.Fatalf("BillTransaction body = %v, %v", billed, err)
	}
	if _, err := c.CustomerAddress(ctx, key, "ctm_2"); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("no address err = %v", err)
	}
}
