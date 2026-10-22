package mcpmanagement

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagementOperationalInspection(t *testing.T) {
	responses := map[string]string{
		"/v1/apps/alpha":                        `{"id":"app-one","slug":"alpha"}`,
		"/v1/apps/alpha/deployments":            `{"items":[{"id":"dep-one","app_id":"app-one","status":"failed","override_env":{"PRIVATE":"hidden"}}],"next_before":"cursor-next"}`,
		"/v1/apps/alpha/instances":              `[{"id":"instance-two","app_id":"app-one","state":"parked","host_ip":"10.0.0.5"},{"id":"instance-one","app_id":"app-one","state":"running","resident":true,"host_ip":"10.0.0.4"}]`,
		"/v1/apps/alpha/health":                 `{"app_id":"app-one","status":"unknown","phase":"observing","evaluated_at":"2026-10-11T10:00:00Z","valid_for_seconds":30,"serving_deployment_ids":["dep-base"],"capacity":{"known":false},"checks":[{"code":"metrics_unavailable","status":"unknown"}]}`,
		"/v1/apps/alpha/routes":                 `{"slug":"alpha","app_id":"app-one","scope":"default","coverage":"deployment_attributed_history","source":"partial","routes":["/orders"],"collectors_expected":2,"collectors_healthy":1}`,
		"/v1/apps/alpha/operational-summary":    `{"version":1,"app_id":"app-one","scope":"default","monitoring":{"available":false,"status":"unknown"},"recovery":{"coverage":"explicit_scope_only","rollbacks_available":true,"rollbacks":[{"id":"rollback-one","status":"blocked","code":"binding_changed"}]}}`,
		"/v1/apps/alpha/rollbacks/rollback-one": `{"id":"rollback-one","app_id":"app-one","status":"blocked","code":"binding_changed","reason":"PRIVATE","blockers":[{"detail":"PRIVATE"}]}`,
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.Header.Get("Authorization") != "Bearer "+testToken {
			t.Error("inspection made a mutation or used wrong credential")
		}
		body, ok := responses[req.URL.Path]
		if !ok {
			t.Errorf("unexpected path: %s", req.URL.Path)
			w.WriteHeader(404)
			return
		}
		if req.URL.Path == "/v1/apps/alpha/instances" && req.URL.Query().Get("history") != "true" {
			t.Error("instance history option was lost")
		}
		if req.URL.Path == "/v1/apps/alpha/deployments" && (req.URL.Query().Get("before") != "cursor-before" || req.URL.Query().Get("limit") != "1") {
			t.Error("app history pagination was lost")
		}
		_, _ = w.Write([]byte(body))
	}))
	defer backend.Close()
	cs, ctx := connectTestServer(t, backend, 0, "")
	for _, tc := range []struct {
		tool        string
		args        map[string]any
		field, want string
	}{
		{"gregale_list_app_deployments", map[string]any{"app": "alpha", "limit": 1, "before": "cursor-before"}, "next_before", `"cursor-next"`},
		{"gregale_list_instances", map[string]any{"app": "alpha", "limit": 1, "history": true}, "next_offset", "1"},
		{"gregale_get_health", map[string]any{"app": "alpha"}, "status", `"unknown"`},
		{"gregale_get_routes", map[string]any{"app": "alpha"}, "source", `"partial"`},
		{"gregale_get_operational_summary", map[string]any{"app": "alpha"}, "version", "1"},
		{"gregale_get_rollback", map[string]any{"app": "alpha", "operation_id": "rollback-one"}, "code", `"binding_changed"`},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			out := callTool(t, ctx, cs, tc.tool, tc.args, false)
			if string(out[tc.field]) != tc.want {
				t.Fatalf("inspection=%s", out)
			}
			for _, value := range out {
				if strings.Contains(string(value), "10.0.0.") || strings.Contains(string(value), "PRIVATE") || strings.Contains(string(value), "hidden") {
					t.Fatalf("inspection exposed private fields: %s", out)
				}
			}
		})
	}
}
