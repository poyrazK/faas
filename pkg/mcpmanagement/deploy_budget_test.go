package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A local deploy applies gregale.yaml's deployment settings through a priced
// PATCH (ADR-20261111114059), so an agent's plan carries their cost: the
// settings are previewed as an update of an existing app, or as a create of a
// new one. A person's key, which no budget limits, sends no preview.
func TestLocalDeployPlanPricesDeploySettings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		exists  bool
		scopes  []string
		preview string // the request previewed; empty for none
	}{
		{"agent, existing app", true, []string{api.ScopeAgent, api.ScopeAppsRead}, "PATCH /v1/apps/alpha"},
		{"agent, new app", false, []string{api.ScopeAgent, api.ScopeAppsRead}, "POST /v1/apps"},
		{"person", true, []string{api.ScopeAdmin}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLocalFixture(t, tc.exists, "")
			var mu sync.Mutex
			var previews []string
			var bodies []map[string]json.RawMessage
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(api.AgentPreviewHeader) == api.AgentPreviewBudget {
					var body map[string]json.RawMessage
					_ = json.NewDecoder(r.Body).Decode(&body)
					mu.Lock()
					previews, bodies = append(previews, r.Method+" "+r.URL.Path), append(bodies, body)
					mu.Unlock()
					budget, remaining := int64(500), int64(500)
					_ = json.NewEncoder(w).Encode(api.AgentBudgetPreview{Preview: true, Charged: true, AddedCents: 1500,
						BudgetCents: &budget, RemainingCents: &remaining, NeedsApproval: true, ApprovalCategory: api.AgentApprovalBudget})
					return
				}
				if r.URL.Path == "/v1/account" {
					_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: "account-one", KeyScopes: tc.scopes})
					return
				}
				f.serveHTTP(w, r)
			}))
			t.Cleanup(backend.Close)
			cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, LocalCLI: f.cli}, "2026-07-28")
			dir := projectDir(t)
			manifest := "schema_version: 1\nlifecycle:\n  execution_mode: service\n  service_replicas:\n    min: 1\n    max: 2\n    desired: 2\n"
			if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			plan := callTool(t, ctx, cs, "gregale_plan_local_deploy", map[string]any{"path": dir, "app": "alpha", "reason": "run as a service"}, false)
			mu.Lock()
			defer mu.Unlock()
			if tc.preview == "" {
				if len(previews) != 0 || plan["agent_budget"] != nil {
					t.Fatalf("a person's plan previewed %v, agent_budget %s", previews, plan["agent_budget"])
				}
				return
			}
			if len(previews) != 1 || previews[0] != tc.preview {
				t.Fatalf("previews = %v, want %s", previews, tc.preview)
			}
			if !strings.Contains(string(bodies[0]["execution_mode"]), "service") || len(bodies[0]["service_replicas"]) == 0 {
				t.Fatalf("previewed body = %v", bodies[0])
			}
			var budget api.AgentBudgetPreview
			if err := json.Unmarshal(plan["agent_budget"], &budget); err != nil || budget.AddedCents != 1500 || !budget.NeedsApproval {
				t.Fatalf("agent_budget = %s (%v)", plan["agent_budget"], err)
			}
		})
	}
}
