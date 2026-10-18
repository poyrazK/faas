package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductCommandJSONInit(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })
	stdout, restore := swapStdout(t)
	defer restore()
	path := filepath.Join(t.TempDir(), "product.json")
	if code := cmdProduct([]string{"init", "--definition", uuid.NewString(), "--file", path}); code != 0 {
		t.Fatalf("init exit code %d", code)
	}
	var receipt struct {
		File   string `json:"file"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil || receipt.File != path || receipt.Status != "created" {
		t.Fatalf("JSON receipt: %s %v", stdout.String(), err)
	}
}

func TestProductCommandValidation(t *testing.T) {
	for _, args := range [][]string{
		nil, {"bogus"}, {"customers"}, {"features", "create"}, {"init"}, {"apply"}, {"usage", "--app", "exports"}, {"usage", "--app", "exports", "--self", "--tenant", "tenant"}, {"usage", "--app", "exports", "--self", "--period", "2026-99"}, {"customers", "assign", "--app", "exports"}, {"start", "--app", "exports", "--feature", "export", "--input", "in.json"}, {"inspect", "--app", "exports", "unexpected"},
		{"init", "--operation", "export", "--definition", uuid.NewString()}, {"init", "--operation", "export", "--scope", "__all__"}, {"explain"}, {"explain", "bad", "--app", "exports"}, {"explain", uuid.NewString(), "--app", "exports", "--after", "-1"},
	} {
		if _, err := parseProductCommand(args); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
	for _, args := range [][]string{{"usage", "--app", "exports", "--self"}, {"features", "list", "--app", "exports"}, {"customers", "assign", "--app", "exports", "--tenant", uuid.NewString(), "--plan", "starter", "--suspended"}, {"start", "--app", "exports", "--feature", "export", "--input", "in.json", "--key", "stable", "--quote", strings.Repeat("a", 64)}} {
		if _, err := parseProductCommand(args); err != nil {
			t.Errorf("rejected %q: %v", args, err)
		}
	}
}

func TestProductNamedCatalogInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "product.json")
	c, err := parseProductCommand([]string{"init", "--operation", "export", "--scope", "production", "--auth-app", "exports-auth", "--file", path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initProductCatalog(c); err != nil {
		t.Fatal(err)
	}
	catalog, err := readProductCatalog(path)
	if err != nil || catalog.Spec.AuthApp != "exports-auth" || catalog.Spec.Features["export"].DefinitionID != "" || catalog.Spec.Features["export"].Operation.Scope != "production" {
		t.Fatalf("named catalog: %+v %v", catalog, err)
	}
}

func TestProductCLIUsesServerBindingAndExplainsCharges(t *testing.T) {
	id := uuid.NewString()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			var req api.ProductStartRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.App != "exports" || req.Feature != "export" || req.DefinitionID != "" || req.ExpectedQuote != strings.Repeat("a", 64) || r.Header.Get("Idempotency-Key") != "same-work" {
				t.Errorf("CLI did not delegate binding: %+v %v", req, err)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(api.OperationAcceptedResponse{ID: id})
		case 2:
			if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/operations/"+id+"/explain") || r.URL.Query().Get("after") != "1" {
				t.Errorf("explain request: %s", r.URL)
			}
			_ = json.NewEncoder(w).Encode(api.ProductOperationExplanation{Evidence: api.ProductReceiptEvidence{OperationID: id, TenantID: "alice", Receipt: api.ProductOperationReceipt{Feature: "export", Plan: "starter", Currency: "EUR", UnitPriceMillicents: 25000}}, EntitlementDecision: "allowed_at_admission", Charge: api.ProductExplainedCharge{State: "settled", AmountMillicents: 25000, Provisional: true}})
		default:
			t.Errorf("unexpected prefetch or extra request: %s", r.URL)
		}
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	client := NewClient(srv.URL, "tenant")
	if _, err := runProductCommand(t.Context(), client, productCommand{verb: "start", app: "exports", feature: "export", input: path, key: "same-work", quote: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	c, err := parseProductCommand([]string{"explain", id, "--app", "exports", "--after", "1"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := runProductCommand(t.Context(), client, c)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := writeProductOutput(&out, value, false); err != nil || !strings.Contains(out.String(), "allowed_at_admission") || !strings.Contains(out.String(), "settled") || !strings.Contains(out.String(), "25000 millicents") {
		t.Fatalf("human explanation: %s %v", out.String(), err)
	}
	out.Reset()
	if err := writeProductOutput(&out, value, true); err != nil {
		t.Fatal(err)
	}
	var view api.ProductOperationExplanation
	if err := json.Unmarshal(out.Bytes(), &view); err != nil || view.Evidence.OperationID != id || view.Charge.AmountMillicents != 25000 {
		t.Fatalf("JSON explanation: %s %v", out.String(), err)
	}
}

func TestProductCatalogInitPreservesExistingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "product.json")
	c := productCommand{file: path, definition: uuid.NewString()}
	if _, err := initProductCatalog(c); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readProductCatalog(path); err != nil {
		t.Fatal(err)
	}
	if _, err := initProductCatalog(c); err == nil {
		t.Fatal("overwrote existing catalog")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("existing file changed")
	}
	if err := os.WriteFile(path, []byte(`{"currency":"EUR","currency":"USD"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProductCatalog(path); err == nil {
		t.Fatal("duplicate contract fields accepted")
	}
}

func TestProductQuoteRequiresExplicitConsentAndCountsAllRevisions(t *testing.T) {
	quote := strings.Repeat("a", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/platform-tenant-self/product" || r.URL.Query().Get("app") != "exports" {
			t.Errorf("quote submitted work or used wrong scope: %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(api.ProductPortal{Quotes: map[string]string{"export": quote}, Customer: api.ProductCustomer{Plan: "starter"}, Catalog: api.ProductCatalog{Revision: "terms", Spec: api.ProductCatalogSpec{Currency: "EUR", Plans: map[string]api.ProductPlan{"starter": {Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}}, Usage: &api.ProductUsage{Period: "2026-10", Lines: []api.ProductUsageLine{{Feature: "export", Revision: "old", Starts: 2}, {Feature: "export", Revision: "new", Starts: 3}}}})
	}))
	defer srv.Close()
	c, err := parseProductCommand([]string{"quote", "--app", "exports", "--feature", "export"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := runProductCommand(t.Context(), NewClient(srv.URL, "tenant"), c)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := writeProductOutput(&out, value, true); err != nil {
		t.Fatal(err)
	}
	var terms struct {
		Quote     string `json:"quote"`
		Remaining int64  `json:"remaining_starts"`
		Price     int64  `json:"unit_price_millicents"`
	}
	if err := json.Unmarshal(out.Bytes(), &terms); err != nil || terms.Quote != quote || terms.Remaining != 5 || terms.Price != 25000 {
		t.Fatalf("incorrect consent terms: %s %v", out.String(), err)
	}
	for _, hash := range []string{"", "bad", strings.Repeat("z", 64)} {
		if _, err := parseProductCommand([]string{"start", "--app", "exports", "--feature", "export", "--input", "input.json", "--key", "stable", "--quote", hash}); err == nil {
			t.Fatalf("accepted unconfirmed quote %q", hash)
		}
	}
}
