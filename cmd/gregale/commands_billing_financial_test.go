package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 530 — CLI requests stay read-only; validation precedes network access.
func TestFinancialCLI(t *testing.T) {
	calls := 0
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Query().Get("month") != "2026-09" || r.Header.Get("Authorization") != "Bearer fp_live_x" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if fail {
			api.WriteProblem(w, api.ErrCapacity("temporarily unavailable"))
			return
		}
		switch r.URL.Path {
		case "/v1/billing/costs":
			_ = json.NewEncoder(w).Encode(api.FinancialCostsResponse{Currency: "EUR", KnownUsageMillicents: 12345, InvoiceReconciliation: "not_reconciled", MissingBillComponents: []string{"tax"}})
		case "/v1/billing/forecast":
			_ = json.NewEncoder(w).Encode(api.FinancialForecastResponse{Currency: "EUR", MissingBillComponents: []string{"tax"}})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stdout, restore := captureStdout(t)
	defer restore()
	stderr, restoreErr := captureStderr(t)
	defer restoreErr()
	for _, command := range []string{"costs", "forecast"} {
		before := len(stdout.Bytes())
		if code := cmdBilling([]string{command, "--month", "2026-09", "--json"}); code != 0 {
			t.Fatalf("%s exit=%d stderr=%s", command, code, stderr)
		}
		var response map[string]json.RawMessage
		if err := json.Unmarshal(stdout.Bytes()[before:], &response); err != nil || string(response["currency"]) != `"EUR"` {
			t.Fatalf("JSON response: %s, %v", stdout, err)
		}
	}
	for _, args := range [][]string{{"--month", "bad"}, {"--month", "9999-01"}, {"unexpected"}, {"--account", "other"}} {
		if code := cmdBillingFinancial("costs", args); code == 0 {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	if calls != 2 {
		t.Fatalf("invalid arguments reached API: %d", calls)
	}
	fail = true
	before := stdout.String()
	if code := cmdBillingFinancial("costs", []string{"--month", "2026-09", "--json"}); code == 0 || stdout.String() != before {
		t.Fatal("failed API emitted successful output")
	}
	if got := financialMoney(12345); got != "EUR 0.12345" {
		t.Fatalf("money rendering lost precision: %s", got)
	}
}
