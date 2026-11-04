package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppAgentMCPUsesNativeBindingWithoutAccountInventory(t *testing.T) {
	scopes := []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}
	f := agentKeyFixture(t, map[string][]string{"fp_test_bound_agent": scopes})
	var globalInventory atomic.Int32
	f.apiHook = func(w http.ResponseWriter, r *http.Request, account string) bool {
		switch r.URL.Path {
		case "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: account, Plan: "pro", Status: "active", KeyScopes: scopes, KeyApp: &api.AppAgentBinding{ID: account + "-app", Slug: "alpha"}})
			return true
		case "/v1/apps":
			globalInventory.Add(1)
			w.WriteHeader(403)
			return true
		}
		return false
	}
	cs, ctx := f.connect(tokenResponse{AccessToken: "fp_test_bound_agent"}, "2026-07-28")
	out := callTool(t, ctx, cs, "gregale_list_apps", map[string]any{}, false)
	var apps []appView
	if err := json.Unmarshal(out["items"], &apps); err != nil || len(apps) != 1 || apps[0].Slug != "alpha" {
		t.Fatalf("inventory=%v %v", apps, err)
	}
	if globalInventory.Load() != 0 {
		t.Fatal("app-agent attempted account inventory")
	}
	denied := callTool(t, ctx, cs, "gregale_get_app", map[string]any{"app": "beta"}, true)
	if toolCode(t, denied) != "app_access_denied" {
		t.Fatalf("cross-app lookup=%v", denied)
	}
	tools := listedTools(t, ctx, cs)
	if tools["gregale_list_deployments"] || tools["gregale_list_environments"] || tools["gregale_plan_env_change"] {
		t.Fatal("app-agent catalog ignored native scope restrictions")
	}
}
