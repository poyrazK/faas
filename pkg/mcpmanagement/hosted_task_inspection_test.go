package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

var taskInspectionTools = []string{"gregale_inspect_task_tool_recovery", "gregale_plan_task_tool_recovery"}

func TestHostedTaskInspectionBothProtocolsAndReconnect(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		t.Run(protocol, func(t *testing.T) {
			record := taskRetirementReceipt(t)
			const token = "fp_test_task_original_requester"
			var mu sync.Mutex
			var reads, writes atomic.Int32
			// No CLI, uploads, publishing consent or connection writes are available.
			f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Hosted: true, AgentKeys: true, AgentKeyToolsetSelection: true, AllowTaskInspection: true, Toolsets: []string{ToolsetAll}})
			f.mu.Lock()
			f.accounts[token] = record.AccountID
			f.keyScopes = map[string][]string{token: {api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}}
			f.apiHook = func(w http.ResponseWriter, r *http.Request, account string) bool {
				if r.URL.Path == "/v1/account" || strings.HasPrefix(r.URL.Path, "/v1/account/") {
					return false
				}
				if r.Method != http.MethodGet {
					writes.Add(1)
					t.Error("Task inspection made a native mutation", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return true
				}
				reads.Add(1)
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/v1/task-tool-release-intents/"+record.ID {
					if r.Header.Get("Authorization") != "Bearer "+token || account != record.AccountID {
						t.Error("inspection substituted the original requester credential")
						w.WriteHeader(404)
						return true
					}
					data, _ := json.Marshal(record)
					var native map[string]any
					_ = json.Unmarshal(data, &native)
					native["private_profile"] = "/private/TASK_BINDINGS_SENTINEL.json"
					native["token"] = "TASK_SECRET_SENTINEL"
					_ = json.NewEncoder(w).Encode(native)
					return true
				}
				// Unavailable native facts remain unknown, never inferred healthy.
				w.WriteHeader(404)
				return true
			}
			f.mu.Unlock()
			for reconnect := range 2 {
				cs, ctx := connectAgentCatalog(t, f, token, ToolsetBuildTools, protocol)
				tools := listedTools(t, ctx, cs)
				for _, name := range taskInspectionTools {
					if !tools[name] {
						t.Fatal("hosted inspection missing", name)
					}
				}
				for name := range tools {
					if strings.Contains(name, "task_tool") && name != taskInspectionTools[0] && name != taskInspectionTools[1] {
						t.Fatal("hosted inspection exposed local or mutating Task tool", name)
					}
				}
				for _, name := range taskInspectionTools {
					body := publicationCall(t, ctx, cs, name, taskReleaseIntentArgs{IntentID: record.ID}, false)
					raw, _ := json.Marshal(body)
					var report tasktoolrelease.RecoveryReport
					if json.Unmarshal(raw, &report) != nil || report.IntentID != record.ID || report.OperationID != record.Execution.OperationID || report.Receipt == nil || report.Receipt.Revision != record.Execution.Receipt.Revision || report.Lifecycle == nil || report.NextStep == nil || !report.ReadOnly || report.ReadyToApply || !report.OwnerAuthorizationRequired {
						t.Fatalf("lost native checkpoint/recovery boundary: %s", raw)
					}
					expectedStatus := record.Status
					if report.IntentStatus != expectedStatus {
						t.Fatalf("stale native status: %s, want %s", report.IntentStatus, expectedStatus)
					}
					for _, private := range []string{record.Execution.ClaimSHA256, record.Execution.BindingsSHA256, record.Execution.AuthorizerKeyID, "TASK_BINDINGS_SENTINEL", "TASK_SECRET_SENTINEL", "claim_sha256", "bindings_sha256", "private_profile"} {
						if private != "" && strings.Contains(string(raw), private) {
							t.Fatal("private native field escaped recovery projection", private)
						}
					}
					unknown := false
					for _, check := range report.Checks {
						unknown = unknown || check.State == "unknown"
					}
					if !unknown || report.ExpiresAt.Sub(report.ObservedAt) != api.TaskToolRecoveryObservationTTL {
						t.Fatal("missing facts or observation expiry lost their bounds")
					}
				}
				// Caller consent cannot turn inspection into an apply or raw journal read.
				for _, name := range []string{"gregale_apply_task_tool_deploy", "gregale_get_task_tool_operation"} {
					result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{"intent_id": record.ID, "operation_id": record.Execution.OperationID, "allow_tool_execution": true}})
					if err == nil && !result.IsError {
						t.Fatal("hosted inspection could call excluded tool", name)
					}
				}
				if err := cs.Close(); err != nil {
					t.Fatal(err)
				}
				if reconnect == 0 {
					mu.Lock()
					record.Status = "revoked"
					mu.Unlock()
				}
			}
			if reads.Load() == 0 || writes.Load() != 0 {
				t.Fatal("inspection did not remain native GET-only", reads.Load(), writes.Load())
			}
			core, coreCtx := connectAgentCatalog(t, f, token, ToolsetCore, protocol)
			for _, name := range taskInspectionTools {
				if listedTools(t, coreCtx, core)[name] {
					t.Fatal("inspection escaped selected catalog", name)
				}
				result, err := core.CallTool(coreCtx, &mcp.CallToolParams{Name: name, Arguments: taskReleaseIntentArgs{IntentID: record.ID}})
				if err == nil && !result.IsError {
					t.Fatal("direct call bypassed catalog selection", name)
				}
			}
			if err := core.Close(); err != nil {
				t.Fatal(err)
			}
			// The bridge verifies the key again on every request, including reconnect.
			f.mu.Lock()
			delete(f.accounts, token)
			f.mu.Unlock()
			response := f.request(http.MethodPost, "/mcp", strings.NewReader(`{}`), "application/json", token)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatal("revoked requester retained hosted access", response.StatusCode)
			}
		})
	}
}

func TestHostedTaskInspectionAccessDenials(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer backend.Close()
	for _, scenario := range []string{"disabled", "oauth", "app", "org", "missing_account", "local_without_cli"} {
		t.Run(scenario, func(t *testing.T) {
			opts := Options{APIBase: backend.URL, AllowTaskInspection: true, agentKey: true, accountID: "account", Toolsets: []string{ToolsetBuildTools}}
			switch scenario {
			case "disabled":
				opts.AllowTaskInspection = false
			case "oauth":
				opts.agentKey = false
				opts.Environments = &EnvironmentConsent{}
			case "app":
				opts.AllowedApps = map[string]string{"alpha": "app"}
			case "org":
				opts.Org = "team"
			case "missing_account":
				opts.accountID = ""
			case "local_without_cli":
				opts.agentKey = false
			}
			_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range taskInspectionTools {
				if m.toolNames[name] {
					t.Fatal("restricted connection advertises inspection", name)
				}
			}
			if _, err := m.inspectTaskRecovery(t.Context(), taskReleaseIntentArgs{IntentID: strings.Repeat("a", 64)}); err == nil {
				t.Fatal("direct inspection bypassed registration guard")
			}
			if _, err := m.planTaskRecovery(t.Context(), taskReleaseIntentArgs{IntentID: strings.Repeat("a", 64)}); err == nil {
				t.Fatal("direct recovery plan bypassed registration guard")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("denied connection reached native API")
	}
}

func TestHostedTaskInspectionKeyScopeAndAccountBoundary(t *testing.T) {
	record := taskIntentReceipt(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(record) }))
	defer backend.Close()
	remote := &remoteServer{opts: HTTPOptions{APIBase: backend.URL, AllowTaskInspection: true}}
	for _, scenario := range []string{"original", "read_scope_only", "project_or_org", "app"} {
		key := &keyConnection{accountID: record.AccountID, client: api.NewClient(backend.URL, testToken), scopes: []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}}
		switch scenario {
		case "read_scope_only":
			key.scopes = []string{api.ScopeAgent, api.ScopeAppsRead}
		case "project_or_org":
			key.publishingLimited = true
		case "app":
			key.allowedApps = map[string]string{"alpha": "app"}
		}
		opts := remote.keyOptions(key)
		if opts.AllowTaskInspection != (scenario == "original") {
			t.Fatal("key restrictions lost", scenario)
		}
	}
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, AllowTaskInspection: true, agentKey: true, accountID: "another-account", Toolsets: []string{ToolsetBuildTools}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.inspectTaskRecovery(t.Context(), taskReleaseIntentArgs{IntentID: record.ID}); err == nil {
		t.Fatal("native response from another account escaped")
	}
}
