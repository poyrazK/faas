package paddleinvoice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// adr: 20261020120000 — the client reads a customer's latest active address
// and business, creates or updates them, and creates a billed transaction
// only with an address.
func TestPaddleCustomerBillingDetails(t *testing.T) {
	var requests []string
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		switch r.Method + " " + r.URL.Path {
		case "GET /customers/ctm_1/addresses":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"add_old","status":"active","country_code":"DE","updated_at":"2026-09-01T00:00:00Z"},
				{"id":"add_new","status":"active","country_code":"US","postal_code":"94107","city":"San Francisco","updated_at":"2026-10-01T00:00:00Z"}]}`)
		case "GET /customers/ctm_2/addresses", "GET /customers/ctm_2/businesses":
			_, _ = fmt.Fprint(w, `{"data":[]}`)
		case "GET /customers/ctm_1/businesses":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"biz_1","status":"active","name":"Acme","tax_identifier":"DE123","updated_at":"2026-10-01T00:00:00Z"}]}`)
		case "POST /customers/ctm_2/addresses":
			_, _ = fmt.Fprint(w, `{"data":{"id":"add_2","status":"active","country_code":"GB"}}`)
		case "PATCH /customers/ctm_1/addresses/add_new":
			_, _ = fmt.Fprint(w, `{"data":{"id":"add_new","status":"active","country_code":"US","postal_code":"94110"}}`)
		case "POST /customers/ctm_2/businesses":
			_, _ = fmt.Fprint(w, `{"data":{"id":"biz_2","status":"active","name":"Beta"}}`)
		case "POST /transactions":
			_, _ = fmt.Fprint(w, `{"data":{"id":"txn_1","status":"billed"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"error":{"code":"not_found","detail":"no such entity"}}`)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()
	const key = "pdl_sdbx_apikey_01abcd"

	address, ok, err := c.ActiveAddress(ctx, key, "ctm_1")
	if err != nil || !ok || address.ID != "add_new" || address.PostalCode != "94107" || requests[0] != "GET /customers/ctm_1/addresses?status=active&per_page=200" {
		t.Fatalf("active address = %+v %v %v %v", address, ok, err, requests)
	}
	business, ok, err := c.ActiveBusiness(ctx, key, "ctm_1")
	if err != nil || !ok || business.ID != "biz_1" || business.TaxIdentifier != "DE123" {
		t.Fatalf("active business = %+v %v %v", business, ok, err)
	}
	if _, ok, err := c.ActiveAddress(ctx, key, "ctm_2"); err != nil || ok {
		t.Fatalf("customer without an address: %v %v", ok, err)
	}

	address.PostalCode, address.SecondLine = "94110", ""
	if updated, err := c.PutAddress(ctx, key, "ctm_1", address); err != nil || updated.PostalCode != "94110" {
		t.Fatalf("update address = %+v %v", updated, err)
	}
	if body := bodies[len(bodies)-1]; body["postal_code"] != "94110" || body["country_code"] != "US" || body["second_line"] != nil {
		t.Fatalf("update body = %v", body)
	}
	if created, err := c.PutAddress(ctx, key, "ctm_2", Address{CountryCode: "GB"}); err != nil || created.ID != "add_2" {
		t.Fatalf("create address = %+v %v", created, err)
	}
	if created, err := c.PutBusiness(ctx, key, "ctm_2", Business{Name: "Beta"}); err != nil || created.ID != "biz_2" ||
		bodies[len(bodies)-1]["name"] != "Beta" || bodies[len(bodies)-1]["tax_identifier"] != nil {
		t.Fatalf("create business = %+v %v %v", created, err, bodies[len(bodies)-1])
	}

	params := TransactionParams{CustomerID: "ctm_1", Currency: "eur", Items: []Item{{Description: "Exports", AmountMinor: 500}}}
	if _, err := c.CreateTransaction(ctx, key, TransactionParams{CustomerID: "ctm_1", Currency: "eur", Bill: true}); err == nil {
		t.Fatal("billed a transaction without an address")
	}
	params.AddressID, params.BusinessID, params.Bill, params.TermsDays = "add_new", "biz_1", true, 14
	if id, err := c.CreateTransaction(ctx, key, params); err != nil || id != "txn_1" {
		t.Fatalf("billed transaction = %q %v", id, err)
	}
	body := bodies[len(bodies)-1]
	terms := body["billing_details"].(map[string]any)["payment_terms"].(map[string]any)
	if body["status"] != "billed" || body["address_id"] != "add_new" || body["business_id"] != "biz_1" || body["collection_mode"] != "manual" || terms["frequency"] != float64(14) {
		t.Fatalf("billed transaction body = %v", body)
	}
}
