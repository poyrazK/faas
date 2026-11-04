// adr: 20261103211147
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func nativePreviewFixture(operation string) api.AppOperationPreview {
	scope := api.ScopeDeployWrite
	if operation == "env_change" {
		scope = api.ScopeEnvWrite
	}
	return api.AppOperationPreview{SchemaVersion: 1, AppID: "app-one", Scope: "default", Operation: operation, Authorization: "allowed", Admission: "unknown", RequiredScopes: []string{scope}, CheckedAt: time.Now().UTC(),
		Checks: map[string]api.AppOperationCheck{"scope": {Status: "allowed", Reason: "required_scope_present"}, "role": {Status: "allowed", Reason: "native_app_role_authorized"}, "email": {Status: "allowed", Reason: "not_required"}, "quota": {Status: "unknown", Reason: "exact_request_required"}}}
}

func TestAgentWorkflowNativeAuthorizationPreview(t *testing.T) {
	base, _, _ := newReleaseFixture(t, false)
	var writes atomic.Int64
	var foreign atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		if r.URL.Path == "/v1/apps/alpha/operation-preview" {
			out := nativePreviewFixture(r.URL.Query().Get("operation"))
			if foreign.Load() {
				out.AppID = "another-app"
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		base.serveHTTP(w, r)
	}))
	defer backend.Close()
	agent := workflowAgent(t, backend, Options{APIBase: backend.URL, Token: testToken, Toolsets: []string{ToolsetRelease}})
	out := agent.call("gregale_preflight", map[string]any{"app": "alpha", "operation": "env_change"}, false)
	var checks map[string]preflightCheck
	if err := json.Unmarshal(out["checks"], &checks); err != nil {
		t.Fatal(err)
	}
	if checks["api_write_authorization"].Status != "passed" || checks["write_consent"].Status != "blocked" || checks["native_admission"].Status != "unknown" {
		t.Fatal("native permission was confused with consent or admission")
	}
	foreign.Store(true)
	out = agent.call("gregale_preflight", map[string]any{"app": "alpha", "operation": "env_change"}, false)
	if err := json.Unmarshal(out["checks"], &checks); err != nil {
		t.Fatal(err)
	}
	if checks["api_write_authorization"].Status != "unknown" || string(out["authorization_preview"]) != "null" {
		t.Fatal("foreign policy snapshot was trusted")
	}
	if writes.Load() != 0 {
		t.Fatal("preflight wrote native intent")
	}
}

func TestAgentWorkflowNativeCostsNeedNoCLI(t *testing.T) {
	base := newLocalFixture(t, true, "release-base")
	var global atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/billing/costs" {
			global.Add(1)
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/v1/apps/alpha/costs" {
			_ = json.NewEncoder(w).Encode(api.AppCostsResponse{AppID: base.app.ID, AppSlug: "alpha", Scope: "application_attributed_retained_compute_and_interface_egress", Currency: "USD", KnownUsageMillicents: 9007199254740993, Meters: []api.AppCostMeter{}, MissingBillComponents: []string{"tax"}})
			return
		}
		base.serveHTTP(w, r)
	}))
	defer backend.Close()
	agent := workflowAgent(t, backend, Options{APIBase: backend.URL, Token: testToken, agentKey: true, Capabilities: []string{api.ScopeUsageRead}, AllowedApps: map[string]string{"alpha": base.app.ID}, Toolsets: []string{ToolsetOperate}})
	result, err := agent.client.CallTool(t.Context(), &mcp.CallToolParams{Name: "gregale_get_costs", Arguments: map[string]any{"app": "alpha"}})
	if err != nil || result.IsError {
		t.Fatalf("native app costs: %v", err)
	}
	// The SDK client's generic map uses float64. Inspect the exact JSON text
	// emitted by the server rather than round-tripping that client-side map.
	var out map[string]json.RawMessage
	if len(result.Content) == 0 {
		t.Fatal("missing cost content")
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	validateWorkflowSchema(t, agent.tools["gregale_get_costs"].OutputSchema, []byte(text))
	if string(out["known_usage_millicents"]) != "9007199254740993" || global.Load() != 0 {
		t.Fatal("native app costs broadened access or rounded monetary amounts")
	}
}

func TestAgentWorkflowNativeCostsRejectForeignAllocationsAndUnavailableAPI(t *testing.T) {
	base := newLocalFixture(t, true, "release-base")
	var mode atomic.Int32
	var global atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/billing/costs" {
			global.Add(1)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path == "/v1/apps/alpha/costs" {
			if mode.Load() == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			out := api.AppCostsResponse{AppID: base.app.ID, AppSlug: "alpha", Scope: "application_attributed_retained_compute_and_interface_egress"}
			allocation := api.FinancialAppCostAllocation{}
			allocation.Attribution.AppID = "foreign-app"
			out.Meters = []api.AppCostMeter{{Allocations: []api.FinancialAppCostAllocation{allocation}}}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		base.serveHTTP(w, r)
	}))
	defer backend.Close()
	agent := workflowAgent(t, backend, Options{APIBase: backend.URL, Token: testToken, agentKey: true, Capabilities: []string{api.ScopeUsageRead}, AllowedApps: map[string]string{"alpha": base.app.ID}, Toolsets: []string{ToolsetOperate}})
	for _, state := range []int32{0, 1} {
		mode.Store(state)
		agent.call("gregale_get_costs", map[string]any{"app": "alpha"}, true)
	}
	if global.Load() != 0 {
		t.Fatal("unavailable or incompatible app costs fell back to account billing")
	}
}

func TestNativeOperationPreviewValidationRejectsStaleOrContradictoryEvidence(t *testing.T) {
	for _, mutate := range []func(*api.AppOperationPreview){
		func(p *api.AppOperationPreview) { p.CheckedAt = time.Now().Add(-2 * time.Minute) },
		func(p *api.AppOperationPreview) { p.CheckedAt = time.Now().Add(2 * time.Minute) },
		func(p *api.AppOperationPreview) {
			p.Checks["scope"] = api.AppOperationCheck{Status: "blocked", Reason: "scope_required"}
		},
		func(p *api.AppOperationPreview) { p.Admission = "allowed" },
		func(p *api.AppOperationPreview) { p.Operation = "scale" },
		func(p *api.AppOperationPreview) { p.RequiredScopes = []string{api.ScopeAdmin} },
	} {
		preview := nativePreviewFixture("env_change")
		mutate(&preview)
		if validOperationPreview(preview, "app-one", "", "", "default", "env_change") {
			t.Fatal("trusted stale or contradictory native authorization")
		}
	}
}
