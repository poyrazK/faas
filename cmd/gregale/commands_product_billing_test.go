package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductBillingCommandContract(t *testing.T) {
	tenant, operation, credit, document := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	hash := strings.Repeat("a", 64)
	for _, tc := range []struct {
		verb         string
		flags        []string
		method, path string
		body         map[string]any
	}{
		{"review", nil, "GET", "review", nil},
		{"list", []string{"--cursor", document}, "GET", "documents", nil},
		{"receipts", nil, "GET", "receipts", nil},
		{"finalize", []string{"--review-hash", hash}, "POST", "statements/finalize", map[string]any{"tenant_id": tenant, "period": "2026-09", "expected_review_hash": hash}},
		{"waive", []string{"--operation", operation, "--reason", "lost exporter outcome"}, "POST", "receipts/" + operation + "/waive", map[string]any{"tenant_id": tenant, "reason": "lost exporter outcome"}},
		{"credit", []string{"--id", credit, "--operation", operation, "--amount-millicents", "5000", "--reason", "service credit"}, "POST", "credits", map[string]any{"id": credit, "tenant_id": tenant, "operation_id": operation, "amount_millicents": json.Number("5000"), "reason": "service credit"}},
		{"handoff", []string{"--document", document, "--invoice-id", "invoice 1/owner"}, "POST", "documents/" + document + "/handoff", map[string]any{"tenant_id": tenant, "external_invoice_id": "invoice 1/owner"}},
		{"handoff-status", []string{"--document", document}, "GET", "documents/" + document + "/handoff", nil},
		{"export", []string{"--document", document, "--provider", "paddle", "--customer", "ctm_01acme"}, "POST", "documents/" + document + "/invoice", map[string]any{"tenant_id": tenant, "provider": "paddle", "customer": "ctm_01acme"}},
		{"export-status", []string{"--document", document}, "GET", "documents/" + document + "/invoice", nil},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != "/v1/apps/exports/product/billing/"+tc.path || r.URL.Query().Get("tenant_id") != tenant || r.URL.Query().Get("period") != "2026-09" {
					t.Errorf("billing request: %s %s", r.Method, r.URL)
				}
				if tc.body != nil {
					var body map[string]any
					decoder := json.NewDecoder(r.Body)
					decoder.UseNumber()
					if err := decoder.Decode(&body); err != nil {
						t.Error(err)
					}
					for key, value := range tc.body {
						if body[key] != value {
							t.Errorf("lost stable request field %s: %v want %v", key, body[key], value)
						}
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": document, "review_hash": hash, "documents": []any{}})
			}))
			defer srv.Close()
			args := append([]string{"statements", tc.verb, "--app", "exports", "--tenant", tenant, "--period", "2026-09"}, tc.flags...)
			c, err := parseProductCommand(args)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := runProductCommand(t.Context(), NewClient(srv.URL, "owner"), c); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 2 {
				t.Fatalf("command added extra requests: %d", calls)
			}
		})
	}
	for _, flags := range [][]string{{"finalize"}, {"credit", "--id", credit, "--operation", operation, "--amount-millicents", "-1", "--reason", "credit"}, {"credit", "--operation", operation, "--amount-millicents", "5000", "--reason", "credit"}, {"waive", "--operation", operation}, {"handoff", "--document", document},
		{"export", "--document", document}, {"export", "--document", document, "--provider", "chargebee"}, {"export", "--provider", "stripe"}} {
		args := append([]string{"statements", flags[0], "--app", "exports", "--tenant", tenant}, flags[1:]...)
		if _, err := parseProductCommand(args); err == nil {
			t.Errorf("accepted unsafe billing command: %q", args)
		}
	}
}

func TestProductBillingCustomerCLI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/platform-tenant-self/product/billing" || r.URL.Query().Get("app") != "exports" || r.URL.Query().Get("period") != "2026-09" || r.URL.Query().Get("tenant_id") != "" {
			t.Errorf("self billing request: %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(api.ProductCustomerBilling{Review: api.ProductBillingReview{TenantID: "authenticated-tenant", AmountMillicents: 25000}, Documents: api.ProductBillingDocuments{Documents: []api.ProductBillingDocument{{Kind: "statement", AmountMillicents: 25000}}}})
	}))
	defer srv.Close()
	for _, verb := range []string{"review", "list"} {
		c, err := parseProductCommand([]string{"statements", verb, "--app", "exports", "--self", "--period", "2026-09"})
		if err != nil {
			t.Fatal(err)
		}
		value, err := runProductCommand(t.Context(), NewClient(srv.URL, "customer"), c)
		if err != nil {
			t.Fatal(err)
		}
		switch got := value.(type) {
		case api.ProductBillingReview:
			if got.TenantID != "authenticated-tenant" {
				t.Fatal("self review lost identity")
			}
		case api.ProductBillingDocuments:
			if len(got.Documents) != 1 || got.Documents[0].Kind != "statement" {
				t.Fatal("self list is still provisional usage")
			}
		default:
			t.Fatalf("unknown self billing view %T", value)
		}
	}
}
