package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestManagementAppConsentFiltersAndGuardsEveryTarget(t *testing.T) {
	var recreated atomic.Bool
	var deniedAppCalls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps":
			_ = json.NewEncoder(w).Encode([]api.AppResponse{{ID: "app-a", Slug: "alpha"}, {ID: "app-b", Slug: "beta"}})
		case "/v1/apps/alpha":
			id := "app-a"
			if recreated.Load() {
				id = "new-app"
			}
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
		case "/v1/deployments/other":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "other", AppID: "app-b"})
		case "/v1/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: "mine", AppID: "app-a"}, {ID: "other", AppID: "app-b"}}})
		default:
			deniedAppCalls.Add(1)
			w.WriteHeader(404)
		}
	}))
	defer backend.Close()
	cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, AllowedApps: map[string]string{"alpha": "app-a"}}, "2026-07-28")
	result := callTool(t, ctx, cs, "gregale_list_apps", map[string]any{}, false)
	if strings.Contains(string(result["items"]), "beta") || string(result["total"]) != "1" {
		t.Fatal("app enumeration escaped consent")
	}
	result = callTool(t, ctx, cs, "gregale_list_deployments", map[string]any{}, false)
	if strings.Contains(string(result["items"]), "other") {
		t.Fatal("deployment enumeration escaped consent")
	}

	for _, test := range []struct {
		tool string
		args map[string]any
	}{
		{"gregale_get_app", map[string]any{"app": "beta"}},
		{"gregale_get_logs", map[string]any{"app": "beta"}},
		{"gregale_get_metrics", map[string]any{"app": "beta"}},
		{"gregale_get_health", map[string]any{"app": "beta"}},
		{"gregale_get_routes", map[string]any{"app": "beta"}},
		{"gregale_get_operational_summary", map[string]any{"app": "beta"}},
		{"gregale_list_instances", map[string]any{"app": "beta"}},
		{"gregale_list_app_deployments", map[string]any{"app": "beta"}},
		{"gregale_plan_deploy", map[string]any{"app": "beta", "image": testImage, "reason": "outside grant"}},
		{"gregale_plan_rollback", map[string]any{"app": "beta", "target_deployment_id": "other", "reason": "outside grant"}},
		{"gregale_wait_deployment", map[string]any{"app": "beta", "deployment_id": "other"}},
		{"gregale_get_rollback", map[string]any{"app": "beta", "operation_id": "one"}},
	} {
		result := callTool(t, ctx, cs, test.tool, test.args, true)
		if string(result["code"]) != `"app_access_denied"` {
			t.Fatalf("%s did not enforce app consent", test.tool)
		}
	}

	callTool(t, ctx, cs, "gregale_get_deployment", map[string]any{"deployment_id": "other"}, true)
	recreated.Store(true)
	callTool(t, ctx, cs, "gregale_get_app", map[string]any{"app": "alpha"}, true)
	if deniedAppCalls.Load() != 0 {
		t.Fatal("outside-app request reached API")
	}
}

func TestManagementAppConsentRejectsRecreationDuringInspection(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/alpha" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id := "app-a"
		if calls.Add(1) > 1 {
			id = "new-app"
		}
		_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
	}))
	defer backend.Close()
	cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, AllowedApps: map[string]string{"alpha": "app-a"}}, "2026-07-28")
	result := callTool(t, ctx, cs, "gregale_get_app", map[string]any{"app": "alpha"}, true)
	if string(result["code"]) != `"app_access_denied"` || strings.Contains(string(result["message"]), "new-app") {
		t.Fatal("recreated app's inspection escaped consent")
	}
}
