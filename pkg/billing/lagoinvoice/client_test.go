package lagoinvoice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// adr: 1507 — one-off invoices use the gregale_usage add-on with skip_psp;
// retries find them by metadata; crashed exports surface untagged invoices.
func TestLagoInvoices(t *testing.T) {
	var created, tagged, addOn map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key-1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprint(w, `{"status":401,"error":"Unauthorized"}`)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/add_ons/gregale_usage":
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"status":404,"error":"Not Found","code":"add_on_not_found"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/add_ons":
			_ = json.NewDecoder(r.Body).Decode(&addOn)
			_, _ = fmt.Fprint(w, `{"add_on":{"code":"gregale_usage"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/invoices":
			_ = json.NewDecoder(r.Body).Decode(&created)
			_, _ = fmt.Fprint(w, `{"invoice":{"lago_id":"inv-1","status":"finalized"}}`)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/invoices/inv-1":
			_ = json.NewDecoder(r.Body).Decode(&tagged)
			_, _ = fmt.Fprint(w, `{"invoice":{"lago_id":"inv-1"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/invoices" && r.URL.Query().Get("metadata[gregale_export_id]") == "exp-1":
			_, _ = fmt.Fprint(w, `{"invoices":[{"lago_id":"inv-1","status":"finalized","metadata":[{"key":"gregale_export_id","value":"exp-1"}]}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/invoices" && r.URL.Query().Get("external_customer_id") == "cust-1":
			_, _ = fmt.Fprint(w, `{"invoices":[
				{"lago_id":"inv-1","invoice_type":"one_off","status":"finalized","fees_amount_cents":375,"metadata":[{"key":"gregale_export_id","value":"exp-1"}]},
				{"lago_id":"inv-2","invoice_type":"one_off","status":"finalized","fees_amount_cents":375,"metadata":[]},
				{"lago_id":"inv-3","invoice_type":"one_off","status":"voided","fees_amount_cents":375,"metadata":[]},
				{"lago_id":"inv-4","invoice_type":"subscription","status":"finalized","fees_amount_cents":375,"metadata":[]}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/invoices/inv-1":
			_, _ = fmt.Fprint(w, `{"invoice":{"lago_id":"inv-1","status":"finalized","payment_status":"pending","payment_due_date":"2026-10-01",
				"total_amount_cents":450,"total_paid_amount_cents":0,"total_due_amount_cents":450,"web_url":"https://lago.example.com/inv-1"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"status":404,"error":"Not Found","code":"invoice_not_found"}`)
		}
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	ctx := context.Background()
	base := srv.URL

	if err := c.EnsureAddOn(ctx, base, "key-1", "eur"); err != nil || addOn["add_on"].(map[string]any)["code"] != AddOnCode {
		t.Fatalf("EnsureAddOn = %v, %v", err, addOn)
	}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	id, err := c.CreateInvoice(ctx, base, "key-1", InvoiceParams{ExternalCustomerID: "cust-1", Currency: "eur",
		Fees: []Fee{{Description: "3 usage units", AmountCents: 375}}, From: from, To: from.AddDate(0, 1, 0)})
	if err != nil || id != "inv-1" {
		t.Fatalf("CreateInvoice = %q, %v", id, err)
	}
	invoice := created["invoice"].(map[string]any)
	fee := invoice["fees"].([]any)[0].(map[string]any)
	if invoice["skip_psp"] != true || invoice["currency"] != "EUR" || fee["unit_amount_cents"] != float64(375) || fee["add_on_code"] != AddOnCode {
		t.Fatalf("invoice body = %v", invoice)
	}
	if _, err := c.CreateInvoice(ctx, base, "key-1", InvoiceParams{Fees: []Fee{{AmountCents: -5}}}); err == nil {
		t.Fatal("negative fee accepted")
	}
	// adr: 1311 — a charging connection lets Lago's payment provider collect.
	if _, err := c.CreateInvoice(ctx, base, "key-1", InvoiceParams{ExternalCustomerID: "cust-1", Currency: "eur", Charge: true,
		Fees: []Fee{{Description: "3 usage units", AmountCents: 375}}, From: from, To: from.AddDate(0, 1, 0)}); err != nil ||
		created["invoice"].(map[string]any)["skip_psp"] != false {
		t.Fatalf("charged invoice = %v, %v", created, err)
	}
	if err := c.SetExportMetadata(ctx, base, "key-1", "inv-1", "exp-1", "st-1"); err != nil || tagged == nil {
		t.Fatalf("SetExportMetadata = %v", err)
	}
	if found, err := c.FindInvoice(ctx, base, "key-1", "exp-1"); err != nil || found != "inv-1" {
		t.Fatalf("FindInvoice = %q, %v", found, err)
	}
	if untagged, err := c.FindUntagged(ctx, base, "key-1", "cust-1", "EUR", from, 375); err != nil || len(untagged) != 1 || untagged[0] != "inv-2" {
		t.Fatalf("FindUntagged = %v, %v", untagged, err)
	}
	st, err := c.GetInvoice(ctx, base, "key-1", "inv-1")
	if err != nil || st.Status != "finalized" || st.DueCents != 450 || st.DueAt == nil || !st.DueAt.Equal(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("GetInvoice = %+v, %v", st, err)
	}
	if gone, err := c.GetInvoice(ctx, base, "key-1", "inv-gone"); err != nil || !gone.Deleted {
		t.Fatalf("missing invoice = %+v, %v", gone, err)
	}
	if err := c.CheckKey(ctx, base, "wrong"); !errors.Is(err, ErrKeyRejected) {
		t.Fatalf("CheckKey = %v, want ErrKeyRejected", err)
	}
}

func TestNormalizeURL(t *testing.T) {
	for raw, want := range map[string]string{
		"":                                 DefaultURL,
		"https://api.eu.getlago.com/":      "https://api.eu.getlago.com",
		"https://lago.example.com/api/v1/": "https://lago.example.com",
		"https://example.com/lago":         "https://example.com/lago",
	} {
		if got, err := NormalizeURL(raw); err != nil || got != want {
			t.Errorf("NormalizeURL(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, bad := range []string{"http://lago.example.com", "https://user:pw@lago.example.com", "https://lago.example.com?x=1", "lago.example.com"} {
		if _, err := NormalizeURL(bad); !errors.Is(err, ErrAPIURL) {
			t.Errorf("NormalizeURL(%q) accepted", bad)
		}
	}
}
