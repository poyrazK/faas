// adr: 20261103193000
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// This deterministic agent driver follows public MCP recovery actions. It
// validates the published schemas with an independent JSON Schema validator,
// records RPC count and catalog bytes, and cannot call an unlisted tool.
type workflowDriver struct {
	t            *testing.T
	client       *mcp.ClientSession
	tools        map[string]*mcp.Tool
	calls        int
	catalogBytes int
}

func workflowAgent(t *testing.T, backend *httptest.Server, opts Options) *workflowDriver {
	t.Helper()
	client, ctx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
	page, err := client.ListTools(ctx, nil)
	if err != nil || page.NextCursor != "" {
		t.Fatalf("catalog: %v", err)
	}
	agent := &workflowDriver{t: t, client: client, tools: map[string]*mcp.Tool{}, calls: 1}
	raw, _ := json.Marshal(page.Tools)
	agent.catalogBytes = len(raw)
	for _, tool := range page.Tools {
		agent.tools[tool.Name] = tool
	}
	return agent
}

func (a *workflowDriver) call(tool string, args map[string]any, wantError bool) map[string]json.RawMessage {
	a.t.Helper()
	if a.tools[tool] == nil {
		a.t.Fatalf("workflow selected unlisted tool %s", tool)
	}
	a.calls++
	result, err := a.client.CallTool(a.t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil || result.IsError != wantError {
		a.t.Fatalf("%s error=%v transport=%v", tool, result, err)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		a.t.Fatal(err)
	}
	validateWorkflowSchema(a.t, a.tools[tool].OutputSchema, raw)
	if result.IsError {
		// Also reject an error that accidentally matched an additive success
		// branch without satisfying the error contract.
		validateWorkflowSchema(a.t, map[string]any{"$defs": map[string]any{"recovery": recoverySchema()}, "anyOf": []any{toolErrorSchema()}}, raw)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		a.t.Fatal(err)
	}
	return fields
}

func validateWorkflowSchema(t *testing.T, schema any, data []byte) {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(value); err != nil {
		t.Fatalf("published response schema: %v", err)
	}
}

func recoveryOf(t *testing.T, fields map[string]json.RawMessage, want string) Recovery {
	t.Helper()
	var recovery Recovery
	if err := json.Unmarshal(fields["recovery"], &recovery); err != nil || recovery.Action != want {
		t.Fatalf("recovery=%+v want=%s err=%v", recovery, want, err)
	}
	return recovery
}

func workflowOptions(backend *httptest.Server, store *StateStore) Options {
	return Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, agentKey: true, Capabilities: []string{api.ScopeEnvWrite}, planStore: store, planNamespace: "workflow-evaluation"}
}

func TestAgentWorkflowDefaultDiscovery(t *testing.T) {
	var writes atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		w.WriteHeader(404)
	}))
	defer backend.Close()
	opts := workflowOptions(backend, nil)
	focused := workflowAgent(t, backend, opts)
	if focused.tools["gregale_plan_env_change"] == nil || focused.tools["gregale_get_plan"] == nil || focused.tools["gregale_plan_deploy"] != nil || focused.tools["gregale_get_trace"] != nil {
		t.Fatal("default catalog is not focused on core workflows")
	}
	result, err := focused.client.CallTool(t.Context(), &mcp.CallToolParams{Name: "gregale_get_trace", Arguments: map[string]any{"app": "alpha"}})
	if err == nil && !result.IsError {
		t.Fatal("unlisted diagnostic tool was callable")
	}
	opts.Toolsets = []string{ToolsetAll}
	full := workflowAgent(t, backend, opts)
	if full.tools["gregale_plan_deploy"] == nil || full.tools["gregale_get_trace"] == nil {
		t.Fatal("explicit all did not opt in")
	}
	if focused.catalogBytes > coreCatalogBudget || focused.catalogBytes*2 >= full.catalogBytes {
		t.Fatalf("catalog byte budget: default=%d all=%d", focused.catalogBytes, full.catalogBytes)
	}
	if writes.Load() != 0 {
		t.Fatal("catalog selection or rejected tool call mutated the native API")
	}
	t.Logf("catalog bytes default=%d all=%d; native writes=%d", focused.catalogBytes, full.catalogBytes, writes.Load())
}

func TestAgentWorkflowReplansRefusedConfiguration(t *testing.T) {
	store, path, key := testStateStore(t)
	f := &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	opts := workflowOptions(backend, store)
	agent := workflowAgent(t, backend, opts)
	input := map[string]any{"app": "alpha", "reason": "workflow configuration", "values": map[string]string{"LOG_LEVEL": "debug"}}
	plan := agent.call("gregale_plan_env_change", input, false)
	id := planID(t, plan)
	f.mu.Lock()
	f.revision++
	f.mu.Unlock()
	refused := agent.call("gregale_apply_env_change", map[string]any{"plan_id": id}, true)
	recovery := recoveryOf(t, refused, "replan")
	if string(refused["outcome"]) != `"refused"` || recovery.Tool != "gregale_plan_env_change" || !recovery.ReuseInputs {
		t.Fatal("known refusal was ambiguous")
	}
	priorCalls := agent.calls
	_ = agent.client.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	opts.planStore = reopened
	agent = workflowAgent(t, backend, opts)
	agent.calls += priorCalls
	receipt := agent.call("gregale_get_plan", map[string]any{"plan_id": id}, false)
	if string(receipt["status"]) != `"refused"` {
		t.Fatal("refused operation reported an unknown outcome")
	}
	agent.call("gregale_apply_env_change", map[string]any{"plan_id": id}, true)
	plan = agent.call(recovery.Tool, input, false)
	agent.call("gregale_apply_env_change", map[string]any{"plan_id": planID(t, plan)}, false)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.puts != 1 || len(f.batchBodies) != 2 || agent.calls > 9 {
		t.Fatalf("recovery budget: writes=%d batches=%d calls=%d", f.puts, len(f.batchBodies), agent.calls)
	}
	if strings.Contains(string(mustJSON(t, receipt)), "debug") || strings.Contains(string(mustJSON(t, refused)), "debug") {
		t.Fatal("recovery exposed private configuration inputs")
	}
	t.Logf("refusal recovery calls=%d writes=%d", agent.calls, f.puts)
}

func TestAgentWorkflowRecoversAfterRestart(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "lost_response", true: "configuration_committed_restart_refused"}[partial], func(t *testing.T) {
			store, path, key := testStateStore(t)
			f := &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}, failBatchAfterCommit: !partial, denyRestart: partial}
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			defer backend.Close()
			opts := workflowOptions(backend, store)
			agent := workflowAgent(t, backend, opts)
			plan := agent.call("gregale_plan_env_change", map[string]any{"app": "alpha", "reason": "recovery evaluation", "values": map[string]string{"LOG_LEVEL": "debug"}, "restart": partial}, false)
			id := planID(t, plan)
			failed := agent.call("gregale_apply_env_change", map[string]any{"plan_id": id}, true)
			wantAction := "retry_same_plan"
			wantStatus := "outcome_unknown"
			if partial {
				wantAction = "request_access"
				wantStatus = "partial"
			}
			recoveryOf(t, failed, wantAction)
			_ = agent.client.Close()
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			opts.planStore = reopened
			restored := workflowAgent(t, backend, opts)
			receipt := restored.call("gregale_get_plan", map[string]any{"plan_id": id}, false)
			if string(receipt["status"]) != `"`+wantStatus+`"` {
				t.Fatalf("restored status=%s", receipt["status"])
			}
			recovery := recoveryOf(t, receipt, wantAction)
			if partial {
				if len(receipt["result"]) == 0 || strings.Contains(string(receipt["result"]), "debug") {
					t.Fatal("partial receipt lost its safe configuration checkpoint")
				}
				f.mu.Lock()
				f.denyRestart = false
				f.mu.Unlock()
				// Access restoration is an external prerequisite; then explicitly resume.
				restored.call("gregale_apply_env_change", map[string]any{"plan_id": id}, false)
			} else {
				restored.call(recovery.Tool, recovery.Arguments, false)
			}
			completed := restored.call("gregale_get_plan", map[string]any{"plan_id": id}, false)
			if string(completed["status"]) != `"completed"` {
				t.Fatal("workflow did not complete")
			}
			recoveryOf(t, completed, "none")
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.puts != 1 || partial && f.restarts != 1 || !partial && f.restarts != 0 || agent.calls+restored.calls > 9 {
				t.Fatalf("resume budget: writes=%d restarts=%d calls=%d", f.puts, f.restarts, agent.calls+restored.calls)
			}
			t.Logf("resume calls=%d writes=%d restarts=%d", agent.calls+restored.calls, f.puts, f.restarts)
		})
	}
}

func TestAgentWorkflowProvisioningRespectsRevocation(t *testing.T) {
	store, _, _ := testStateStore(t)
	f := &nativeProvisionFixture{base: newLocalFixture(t, true, ""), fail: "waiting_resource", ready: true}
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	opts := workflowOptions(backend, store)
	opts.Toolsets = []string{ToolsetCore, ToolsetProvision}
	opts.Capabilities = append(opts.Capabilities, api.ScopeManagedPostgresManage)
	agent := workflowAgent(t, backend, opts)
	plan := agent.call("gregale_plan_postgres", map[string]any{"app": "alpha", "region": "us-east-1", "reason": "workflow provisioning"}, false)
	id := planID(t, plan)
	agent.call("gregale_apply_provision", map[string]any{"plan_id": id}, true)
	status := agent.call("gregale_get_provision", map[string]any{"plan_id": id}, false)
	recovery := recoveryOf(t, status, "retry_same_plan")
	f.mu.Lock()
	f.deny = true
	f.mu.Unlock()
	agent.call(recovery.Tool, recovery.Arguments, false)
	deadline := time.Now().Add(5 * time.Second)
	for {
		status = agent.call("gregale_get_provision", map[string]any{"plan_id": id}, false)
		if string(status["status"]) != `"running"` {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("revoked workflow did not stop")
		}
		poll := recoveryOf(t, status, "inspect")
		if poll.AfterSeconds == nil || *poll.AfterSeconds != 2 {
			t.Fatal("missing bounded polling guidance")
		}
		timer := time.NewTimer(time.Duration(*poll.AfterSeconds) * time.Second)
		select {
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("workflow cancelled")
		case <-timer.C:
		}
	}
	recoveryOf(t, status, "request_access")
	if _, ok := status["next"]; ok {
		t.Fatal("revoked workflow still advised a write")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.creates != 1 || f.binds != 0 || agent.calls > 12 {
		t.Fatalf("revocation budget: creates=%d binds=%d calls=%d", f.creates, f.binds, agent.calls)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
