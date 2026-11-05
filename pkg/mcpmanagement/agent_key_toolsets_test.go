// adr: 20261105133521
package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

type catalogTestTransport struct {
	base            http.RoundTripper
	token, toolsets string
}

func (tr catalogTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+tr.token)
	if tr.toolsets != "" {
		clone.Header.Set(agentKeyToolsetsHeader, tr.toolsets)
	}
	return tr.base.RoundTrip(clone)
}

func connectAgentCatalog(t *testing.T, f *remoteFixture, token, selection, version string) (*mcp.ClientSession, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	client := *f.client
	client.Transport = catalogTestTransport{base: f.client.Transport, token: token, toolsets: selection}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "catalog-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.resource, HTTPClient: &client, DisableStandaloneSSE: true}, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, ctx
}

func TestAgentKeyCatalogSelectionSharesPlans(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			store, _, _ := testStateStore(t)
			f := newRemoteFixtureWithOptions(t, true, HTTPOptions{AgentKeys: true, AgentKeyToolsetSelection: true, Toolsets: []string{ToolsetAll}, StateStore: store})
			const token = "fp_test_catalog_agent"
			var writes atomic.Int32
			f.mu.Lock()
			f.accounts[token] = "alice"
			f.keyScopes = map[string][]string{}
			f.keyScopes[token] = []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}
			f.apiHook = func(w http.ResponseWriter, req *http.Request, _ string) bool {
				if req.URL.Path != "/v1/apps/alpha/scaling" {
					return false
				}
				if req.Method != http.MethodGet {
					writes.Add(1)
					api.WriteProblem(w, api.NewProblem(http.StatusConflict, "scaling_revision_conflict", "Scaling changed", "review current settings"))
					return true
				}
				_ = json.NewEncoder(w).Encode(api.AppScalingSnapshot{AppID: "alice-app", Scope: "default", Revision: 1, Settings: api.AppScalingSettings{RAMMB: 256, CPUMillicores: 250, MaxConcurrency: 10}})
				return true
			}
			f.mu.Unlock()
			core, coreCtx := connectAgentCatalog(t, f, token, "", protocol)
			operate, operateCtx := connectAgentCatalog(t, f, token, "operate", protocol)
			coreTools, operateTools := listedTools(t, coreCtx, core), listedTools(t, operateCtx, operate)
			if coreTools["gregale_plan_scale"] || !coreTools["gregale_get_plan"] || !operateTools["gregale_plan_scale"] || operateTools["gregale_get_app"] {
				t.Fatal("catalog selection not applied")
			}
			page, err := core.ListTools(coreCtx, nil)
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := json.Marshal(page.Tools)
			if err != nil || len(catalog) > coreCatalogBudget {
				t.Fatalf("focused agent catalog bytes=%d err=%v", len(catalog), err)
			}
			var wg sync.WaitGroup
			for _, c := range []struct {
				cs        *mcp.ClientSession
				ctx       context.Context
				wantScale bool
			}{{core, coreCtx, false}, {operate, operateCtx, true}} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < 5; i++ {
						page, err := c.cs.ListTools(c.ctx, nil)
						if err != nil {
							t.Error(err)
							return
						}
						found := false
						for _, tool := range page.Tools {
							if tool.Name == "gregale_plan_scale" {
								found = true
							}
						}
						if found != c.wantScale {
							t.Error("concurrent selections crossed catalogs")
							return
						}
					}
				}()
			}
			wg.Wait()
			callTool(t, coreCtx, core, "gregale_plan_scale", map[string]any{"app": "alpha", "reason": "test", "ram_mb": 512}, true)
			plan := callTool(t, operateCtx, operate, "gregale_plan_scale", map[string]any{"app": "alpha", "reason": "test", "ram_mb": 512}, false)
			var id string
			if json.Unmarshal(plan["plan_id"], &id) != nil || id == "" {
				t.Fatal("plan missing")
			}
			receipt := callTool(t, coreCtx, core, "gregale_get_plan", map[string]any{"plan_id": id}, false)
			if string(receipt["plan_id"]) != `"`+id+`"` {
				t.Fatal("catalog selections did not share the original plan")
			}
			callTool(t, coreCtx, core, "gregale_apply_operation", map[string]any{"plan_id": id}, true)
			if writes.Load() != 0 {
				t.Fatal("an unselected mutation reached the native API")
			}
			callTool(t, operateCtx, operate, "gregale_apply_operation", map[string]any{"plan_id": id}, true)
			refused := callTool(t, coreCtx, core, "gregale_get_plan", map[string]any{"plan_id": id}, false)
			var recovery Recovery
			if json.Unmarshal(refused["recovery"], &recovery) != nil || recovery.Action != "replan" || recovery.Tool != "" || len(recovery.Toolsets) != 1 || recovery.Toolsets[0] != ToolsetOperate {
				t.Fatalf("core plan recovery suggested an unlisted tool: %s", refused["recovery"])
			}
			ctxInfo := callTool(t, coreCtx, core, "gregale_context", map[string]any{}, false)
			if string(ctxInfo["toolsets"]) != `["core"]` {
				t.Fatalf("context reported %s", ctxInfo["toolsets"])
			}
			handler, err := NewHTTPHandler(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			f.setHandler(handler)
			restored := callTool(t, coreCtx, core, "gregale_get_plan", map[string]any{"plan_id": id}, false)
			if string(restored["plan_id"]) != `"`+id+`"` || string(restored["status"]) != `"refused"` {
				t.Fatal("catalog selection lost the plan after bridge restart")
			}
		})
	}
}

func TestAgentKeyCatalogRecoveryMetadata(t *testing.T) {
	m := &management{opts: Options{Toolsets: []string{ToolsetAll}, agentKeyToolsetSelection: true}}
	ctx := context.WithValue(t.Context(), agentKeyToolsetsContextKey{}, []string{ToolsetCore})
	data := json.RawMessage(`{"large":9007199254740993,"recovery":{"action":"retry_same_plan","tool":"gregale_apply_operation","arguments":{"plan_id":"p"}},"action":{"retry_tool":"gregale_apply_operation","next_step":"retry_same_plan_with_current_authorization"}}`)
	out, err := m.selectedRecoveryOutput(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Large    json.Number `json:"large"`
		Recovery Recovery    `json:"recovery"`
		Action   struct {
			RetryTool string `json:"retry_tool"`
			NextStep  string `json:"next_step"`
		} `json:"action"`
	}
	if json.Unmarshal(out, &result) != nil || result.Large.String() != "9007199254740993" || result.Recovery.Tool != "" || len(result.Recovery.Arguments) != 0 || len(result.Recovery.Toolsets) != 1 || result.Recovery.Toolsets[0] != ToolsetOperate || result.Action.RetryTool != "" || result.Action.NextStep != "inspect_native_state_before_replanning" {
		t.Fatalf("recovery did not respect selected catalog: %s", out)
	}
}

func TestAgentKeyCatalogSelectionDoesNotGrantWrites(t *testing.T) {
	f := newRemoteFixtureWithOptions(t, true, HTTPOptions{AgentKeys: true, AgentKeyToolsetSelection: true, Toolsets: []string{ToolsetAll}})
	const token = "fp_test_readonly_catalog"
	f.mu.Lock()
	f.keyScopes = map[string][]string{token: {api.ScopeAgent, api.ScopeAppsRead}}
	f.accounts[token] = "alice"
	f.mu.Unlock()
	client, ctx := connectAgentCatalog(t, f, token, "all", "2026-07-28")
	tools := listedTools(t, ctx, client)
	if tools["gregale_apply_operation"] || tools["gregale_apply_deploy"] {
		t.Fatal("catalog selection granted write tools")
	}
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_operation", Arguments: map[string]any{"plan_id": strings.Repeat("a", 64)}})
	if err == nil && result != nil && !result.IsError {
		t.Fatal("read-only credential called a write")
	}
}

func TestOAuthCatalogIgnoresAgentKeySelectionHeader(t *testing.T) {
	f := newRemoteFixtureWithOptions(t, true, HTTPOptions{AgentKeys: true, AgentKeyToolsetSelection: true, Toolsets: []string{ToolsetAll}, Onboarding: "api-key"})
	flow := f.begin(scopeRead)
	review := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"verify"}, "api_key": {"fp_test_alice_remote_key"}, "toolsets": {"core"}}, f.server.URL)
	if review.StatusCode != http.StatusOK {
		t.Fatalf("review=%d", review.StatusCode)
	}
	_ = review.Body.Close()
	approved := f.consent(flow, "approve", "", f.server.URL, flow.csrf, false)
	redirect, err := url.Parse(approved.Header.Get("Location"))
	_ = approved.Body.Close()
	if err != nil || approved.StatusCode != http.StatusSeeOther {
		t.Fatalf("approve=%d error=%v", approved.StatusCode, err)
	}
	response := f.exchange(redirect.Query().Get("code"), testVerifier, f.resource, f.clientID)
	var tokens tokenResponse
	decodeResponse(t, response, &tokens)
	_ = response.Body.Close()
	client, ctx := connectAgentCatalog(t, f, tokens.AccessToken, "all", "2026-07-28")
	tools := listedTools(t, ctx, client)
	if !tools["gregale_get_app"] || tools["gregale_plan_scale"] || tools["gregale_get_trace"] {
		t.Fatal("header widened the OAuth grant's selected catalog")
	}
}

func TestAgentKeyCatalogSelectionKeepsAppBinding(t *testing.T) {
	f := newRemoteFixtureWithOptions(t, true, HTTPOptions{AgentKeys: true, AgentKeyToolsetSelection: true, Toolsets: []string{ToolsetAll}})
	const token = "fp_test_bound_catalog"
	f.mu.Lock()
	f.accounts[token] = "alice"
	f.keyScopes = map[string][]string{token: {api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}}
	f.apiHook = func(w http.ResponseWriter, req *http.Request, _ string) bool {
		if req.URL.Path != "/v1/account" {
			return false
		}
		_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: "alice", KeyScopes: []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}, KeyApp: &api.AppAgentBinding{ID: "alice-app", Slug: "alpha"}})
		return true
	}
	f.mu.Unlock()
	client, ctx := connectAgentCatalog(t, f, token, "all", "2026-07-28")
	tools := listedTools(t, ctx, client)
	if tools["gregale_list_deployments"] || tools["gregale_list_environments"] {
		t.Fatal("catalog selection exposed account or project inventory")
	}
	callTool(t, ctx, client, "gregale_get_app", map[string]any{"app": "other"}, true)
}

func TestAgentKeyCatalogSelectionWithUnspecifiedMaximum(t *testing.T) {
	f := newRemoteFixtureWithOptions(t, true, HTTPOptions{AgentKeys: true, AgentKeyToolsetSelection: true})
	const token = "fp_test_unrestricted_catalog"
	f.mu.Lock()
	f.accounts[token] = "alice"
	f.keyScopes = map[string][]string{token: {api.ScopeAgent, api.ScopeAppsRead}}
	f.mu.Unlock()
	f.opts.Toolsets = nil
	handler, err := NewHTTPHandler(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(handler)
	client, ctx := connectAgentCatalog(t, f, token, "operate", "2026-07-28")
	if !listedTools(t, ctx, client)["gregale_plan_scale"] {
		t.Fatal("nil operator maximum unexpectedly restricted selection")
	}
}

func TestAgentKeyCatalogSelectionRejectsInvalidOrDisallowed(t *testing.T) {
	f := newRemoteFixtureWithOptions(t, true, HTTPOptions{AgentKeys: true, AgentKeyToolsetSelection: true, Toolsets: []string{ToolsetCore}})
	const token = "fp_test_catalog_agent"
	f.mu.Lock()
	f.keyScopes = map[string][]string{}
	f.accounts[token], f.keyScopes[token] = "alice", []string{api.ScopeAgent, api.ScopeAppsRead}
	f.mu.Unlock()
	client, ctx := connectAgentCatalog(t, f, token, "all", "2026-07-28")
	if tools := listedTools(t, ctx, client); !tools["gregale_get_app"] || tools["gregale_plan_scale"] {
		t.Fatal("all expanded the operator ceiling")
	}
	for _, selection := range []string{"operate", "core,unknown", strings.Repeat("x", 257)} {
		req, _ := http.NewRequest(http.MethodPost, f.resource, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(agentKeyToolsetsHeader, selection)
		response, err := f.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("selection %q status=%d", selection, response.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, f.resource, strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Add(agentKeyToolsetsHeader, "core")
	req.Header.Add(agentKeyToolsetsHeader, "operate")
	response, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate catalog header status=%d", response.StatusCode)
	}
}
