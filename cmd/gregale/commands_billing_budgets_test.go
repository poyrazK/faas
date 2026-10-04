package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// adr: 530 — all CLI policy mutations carry explicit optimistic revisions;
// retries can use one stable key, and machine output preserves readiness.
func TestFinancialBudgetCRUDCLI(t *testing.T) {
	id := "6dc4f678-5766-4a06-a061-845c2b133fdd"
	spec := financial.BudgetSpec{Name: "draft", Scope: financial.BudgetScope{Kind: "account"}, Currency: "EUR", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 1000001, NotifyMillicents: []int64{}, Mode: "monitored", Action: "stop_previews", DrainSeconds: 30, ResumeRule: "manual", Enabled: false}
	file := filepath.Join(t.TempDir(), "draft.json")
	data, _ := json.Marshal(spec)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != method || r.URL.Path != path || r.Header.Get("Authorization") != "Bearer fp_live_x" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		p := api.FinancialBudgetResponse{ID: id, Revision: 7, Spec: spec, Status: "draft", EnforcementReady: false, Reasons: []string{"enforcement_integration_pending"}}
		if r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE" {
			if r.Header.Get("Idempotency-Key") != "stable-retry" {
				t.Errorf("operation key: %q", r.Header.Get("Idempotency-Key"))
			}
			var body struct {
				ExpectedRevision int64                `json:"expected_revision"`
				Spec             financial.BudgetSpec `json:"spec"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if r.Method != "POST" && body.ExpectedRevision != 7 {
				t.Errorf("expected revision lost: %+v", body)
			}
			if r.Method != "DELETE" && (body.Spec.LimitMillicents != 1000001 || body.Spec.Enabled) {
				t.Errorf("draft mutated: %+v", body)
			}
		}
		switch {
		case r.URL.Path == "/v1/billing/budgets" && r.Method == "GET":
			_ = json.NewEncoder(w).Encode(api.FinancialBudgetListResponse{Budgets: []api.FinancialBudgetResponse{p}})
		case r.URL.Path == "/v1/billing/budgets/"+id+"/revisions":
			if r.URL.Query().Get("after_revision") != "3" || r.URL.Query().Get("limit") != "2" {
				t.Errorf("history cursor: %s", r.URL)
			}
			_ = json.NewEncoder(w).Encode(api.FinancialBudgetHistoryResponse{Revisions: []api.FinancialBudgetRevisionResponse{}, NextRevision: 5})
		default:
			_ = json.NewEncoder(w).Encode(p)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stdout, restore := captureStdout(t)
	defer restore()
	_, restoreErr := captureStderr(t)
	defer restoreErr()
	for _, tc := range []struct {
		args         []string
		method, path string
	}{
		{[]string{"list", "--json"}, "GET", "/v1/billing/budgets"},
		{[]string{"get", "--json", id}, "GET", "/v1/billing/budgets/" + id},
		{[]string{"create", "--file", file, "--key", "stable-retry", "--json"}, "POST", "/v1/billing/budgets"},
		{[]string{"update", "--file", file, "--expected-revision", "7", "--key", "stable-retry", "--json", id}, "PUT", "/v1/billing/budgets/" + id},
		{[]string{"delete", "--expected-revision", "7", "--key", "stable-retry", "--json", id}, "DELETE", "/v1/billing/budgets/" + id},
		{[]string{"history", "--after-revision", "3", "--limit", "2", "--json", id}, "GET", "/v1/billing/budgets/" + id + "/revisions"},
	} {
		method, path = tc.method, tc.path
		offset := len(stdout.Bytes())
		if code := cmdBilling(append([]string{"budgets"}, tc.args...)); code != 0 {
			t.Fatalf("%v exit %d", tc.args, code)
		}
		if !json.Valid(stdout.Bytes()[offset:]) {
			t.Fatalf("machine output: %s", stdout)
		}
	}
	for _, bad := range [][]string{{"get", "not-an-id"}, {"update", "--file", file, id}, {"delete", "--expected-revision", "-1", id}, {"history", "--limit", "101", id}, {"create"}, {"list", "extra"}} {
		if cmdBillingBudgets(bad) == 0 {
			t.Fatalf("invalid args accepted: %v", bad)
		}
	}
	if calls != 6 {
		t.Fatalf("invalid arguments reached API: %d", calls)
	}
}
