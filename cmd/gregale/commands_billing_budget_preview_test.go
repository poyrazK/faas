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

// adr: 530 — preview must validate a bounded spec before auth or network.
func TestFinancialBudgetPreviewCLI(t *testing.T) {
	spec := financial.BudgetSpec{Name: "preview guard", Scope: financial.BudgetScope{Kind: "account"}, Currency: "EUR", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 1000, NotifyMillicents: []int64{800}, Mode: "monitored", Action: "stop_previews", DrainSeconds: 30, ResumeRule: "manual", Enabled: true}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/billing/budgets/preview" || r.Header.Get("Authorization") != "Bearer fp_live_x" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		var request api.FinancialBudgetPreviewRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Spec.Action != spec.Action {
			t.Errorf("intent: %+v,%v", request, err)
		}
		_ = json.NewEncoder(w).Encode(api.FinancialBudgetPreviewResponse{Spec: spec, KnownMillicents: 75, EnforcementReady: false, Reasons: []string{"enforcement_integration_pending"}, Targets: []api.FinancialBudgetTarget{}, ContinuingTargets: []api.FinancialBudgetTarget{}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stdout, restore := captureStdout(t)
	defer restore()
	_, restoreErr := captureStderr(t)
	defer restoreErr()
	path := filepath.Join(t.TempDir(), "budget.json")
	data, _ := json.Marshal(spec)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if code := cmdBilling([]string{"budget-preview", "--file", path, "--json"}); code != 0 {
		t.Fatalf("preview exit=%d", code)
	}
	var response api.FinancialBudgetPreviewResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || response.KnownMillicents != 75 || response.EnforcementReady {
		t.Fatalf("response: %+v,%v", response, err)
	}
	link := filepath.Join(t.TempDir(), "budget-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if code := cmdBillingBudgetPreview([]string{"--file", link}); code == 0 {
		t.Fatal("symlinked budget spec accepted")
	}
	for _, bad := range [][]byte{[]byte(`{"name":"missing"}`), append(append([]byte{}, data...), []byte(` {}`)...), make([]byte, api.FinancialBudgetSpecBytes+1)} {
		if err := os.WriteFile(path, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if code := cmdBillingBudgetPreview([]string{"--file", path}); code == 0 {
			t.Fatal("invalid spec accepted")
		}
	}
	if calls != 1 {
		t.Fatalf("invalid input reached API: %d", calls)
	}
	if code := cmdBillingBudgetPreview([]string{"--help"}); code != 0 {
		t.Fatalf("help exit=%d", code)
	}
}
