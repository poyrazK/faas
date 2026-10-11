package stripeinvoice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// adr: 976
func TestClientRequestsAndErrors(t *testing.T) {
	var gotForm map[string]string
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		_ = r.ParseForm()
		gotForm = map[string]string{}
		for k := range r.PostForm {
			gotForm[k] = r.PostForm.Get(k)
		}
		switch {
		case r.URL.Path == "/v1/invoices" && r.Method == http.MethodPost:
			_, _ = fmt.Fprint(w, `{"id":"in_1","livemode":false,"total":0}`)
		case r.URL.Path == "/v1/invoiceitems" && r.Method == http.MethodGet:
			if r.URL.Query().Get("starting_after") == "" {
				_, _ = fmt.Fprint(w, `{"data":[{"id":"ii_1","metadata":{"gregale_line":"0"}}],"has_more":true}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"data":[{"id":"ii_2","metadata":{"gregale_line":"1"}},{"id":"ii_x","metadata":{}}],"has_more":false}`)
		case r.URL.Path == "/v1/invoices" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"Invalid API Key provided"}}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error","code":"resource_missing","param":"customer","message":"No such customer: 'cus_x'"}}`)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()

	inv, err := c.CreateInvoice(ctx, "rk_test_abcd", "idem-1", InvoiceParams{Customer: "cus_1", Currency: "EUR", Description: "Usage", Metadata: map[string]string{"k": "v"}})
	if err != nil || inv.ID != "in_1" {
		t.Fatalf("CreateInvoice = %+v, %v", inv, err)
	}
	if gotHeaders.Get("Authorization") != "Bearer rk_test_abcd" || gotHeaders.Get("Idempotency-Key") != "idem-1" || gotHeaders.Get("Stripe-Version") != APIVersion {
		t.Fatalf("headers = %v", gotHeaders)
	}
	if gotForm["currency"] != "eur" || gotForm["auto_advance"] != "false" || gotForm["pending_invoice_items_behavior"] != "exclude" || gotForm["metadata[k]"] != "v" {
		t.Fatalf("invoice form = %v", gotForm)
	}
	lines, err := c.ExportedLines(ctx, "rk_test_abcd", "in_1")
	if err != nil || !lines["0"] || !lines["1"] || len(lines) != 2 {
		t.Fatalf("ExportedLines = %v, %v", lines, err)
	}
	if err := c.CheckKey(ctx, "sk_test_bad1"); !errors.Is(err, ErrKeyRejected) {
		t.Fatalf("CheckKey err = %v, want ErrKeyRejected", err)
	}
	err = c.CreateInvoiceItem(ctx, "rk_test_abcd", "idem-2", ItemParams{Customer: "cus_x", InvoiceID: "in_1", Currency: "EUR", AmountMinor: -3,
		PeriodStart: time.Unix(0, 0), PeriodEnd: time.Unix(60, 0)})
	var stripeErr *Error
	if !errors.As(err, &stripeErr) || stripeErr.Status != http.StatusBadRequest || stripeErr.Param != "customer" {
		t.Fatalf("CreateInvoiceItem err = %v", err)
	}
	if gotForm["amount"] != "-3" || gotForm["period[end]"] != "60" {
		t.Fatalf("item form = %v", gotForm)
	}
	for key, want := range map[string]bool{"rk_live_x": true, "sk_test_x": false} {
		if live, err := KeyLivemode(key); err != nil || live != want {
			t.Fatalf("KeyLivemode(%s) = %v, %v", key, live, err)
		}
	}
	if _, err := KeyLivemode("pk_live_x"); !errors.Is(err, ErrKeyFormat) {
		t.Fatalf("publishable key accepted: %v", err)
	}
}
