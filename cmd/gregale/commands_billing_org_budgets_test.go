package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261031211500 — --org manages the budgets of an organization that
// owns its resources, through the organization's billing routes.
func TestFinancialBudgetCLIOrg(t *testing.T) {
	id := "6dc4f678-5766-4a06-a061-845c2b133fdd"
	for _, tc := range []struct {
		name, method, path string
		args               []string
		response           any
	}{
		{"list", "GET", "/v1/orgs/acme/billing/budgets", []string{"list", "--org", "acme"}, api.FinancialBudgetListResponse{}},
		{"get", "GET", "/v1/orgs/acme/billing/budgets/" + id, []string{"get", "--org", "acme", id}, api.FinancialBudgetResponse{ID: id, Revision: 1}},
		{"delete", "DELETE", "/v1/orgs/acme/billing/budgets/" + id, []string{"delete", "--org", "acme", "--expected-revision", "1", id}, api.FinancialBudgetResponse{ID: id, Revision: 2}},
		{"history", "GET", "/v1/orgs/acme/billing/budgets/" + id + "/revisions", []string{"history", "--org", "acme", id}, api.FinancialBudgetHistoryResponse{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := ""
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.Method + " " + r.URL.Path
				_ = json.NewEncoder(w).Encode(tc.response)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			main.ResetJSONOutput()
			t.Cleanup(main.ResetJSONOutput)
			original := (*main.OsStdout)
			(*main.OsStdout) = io.Discard
			t.Cleanup(func() { (*main.OsStdout) = original })
			if code := main.CmdBillingBudgets(tc.args); code != 0 {
				t.Fatalf("billing budgets %v = %d", tc.args, code)
			}
			if want := tc.method + " " + tc.path; seen != want {
				t.Fatalf("request = %q, want %q", seen, want)
			}
		})
	}
}
