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

// adr: 20261018212918 — billing overdue-policy inspect reads the policy and
// its holds (a missing policy is null, not an error), set sends the day count
// and clear deletes the policy.
func TestProductOverduePolicyCLI(t *testing.T) {
	tenant := uuid.NewString()
	var policy *api.ProductOverduePolicy
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/exports/product/billing/overdue-policy":
			if policy == nil {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no policy"))
				return
			}
			_ = json.NewEncoder(w).Encode(policy)
		case "PUT /v1/apps/exports/product/billing/overdue-policy":
			var req api.PutProductOverduePolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BlockOverdueAfterDays == nil {
				t.Errorf("policy body: %+v %v", req, err)
				return
			}
			policy = &api.ProductOverduePolicy{AppID: "app", BlockOverdueAfterDays: *req.BlockOverdueAfterDays}
			_ = json.NewEncoder(w).Encode(policy)
		case "DELETE /v1/apps/exports/product/billing/overdue-policy":
			policy = nil
			w.WriteHeader(http.StatusNoContent)
		case "GET /v1/apps/exports/product/billing/overdue-holds":
			holds := api.ProductOverdueHolds{Holds: []api.ProductOverdueHold{}}
			if policy != nil {
				holds.Holds = append(holds.Holds, api.ProductOverdueHold{TenantID: tenant})
			}
			_ = json.NewEncoder(w).Encode(holds)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
	}))
	defer srv.Close()
	run := func(args ...string) any {
		t.Helper()
		c, err := parseProductCommand(append([]string{"billing", "overdue-policy"}, append(args, "--app", "exports")...))
		if err != nil {
			t.Fatal(err)
		}
		value, err := runProductCommand(t.Context(), NewClient(srv.URL, "owner"), c)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if view := run("inspect").(productOverdueView); view.Policy != nil || view.Holds == nil || len(view.Holds) != 0 {
		t.Fatalf("inspect without a policy = %+v", view)
	}
	if got := run("set", "--block-overdue-after-days", "0").(api.ProductOverduePolicy); got.BlockOverdueAfterDays != 0 || policy == nil {
		t.Fatalf("set = %+v", got)
	}
	if view := run("inspect").(productOverdueView); view.Policy == nil || len(view.Holds) != 1 || view.Holds[0].TenantID != tenant {
		t.Fatalf("inspect = %+v", view)
	}
	run("clear")
	if policy != nil || requests[len(requests)-1] != "DELETE /v1/apps/exports/product/billing/overdue-policy" {
		t.Fatalf("clear left %+v after %v", policy, requests)
	}
	for _, args := range [][]string{
		{"billing", "overdue-policy", "set", "--app", "exports"},
		{"billing", "overdue-policy", "set", "--app", "exports", "--block-overdue-after-days", "366"},
		{"billing", "overdue-policy", "set", "--app", "exports", "--block-overdue-after-days", "-2"},
		{"billing", "overdue-policy", "remove", "--app", "exports"},
		{"billing", "invoices", "list", "--app", "exports"},
		{"billing", "overdue-policy"},
	} {
		if _, err := parseProductCommand(args); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
}
