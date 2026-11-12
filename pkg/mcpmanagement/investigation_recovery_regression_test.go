package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPRegressionCronUpdateLostResponse(t *testing.T) {
	f := newLocalFixture(t, true, "release-base")
	var mu sync.Mutex
	cron := api.CronResponse{ID: "cron-one", AppID: f.app.ID, Schedule: "*/15 * * * *", Path: "/tick", Enabled: true, Timezone: "UTC"}
	writes := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/crons/cron-one" {
			f.serveHTTP(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPatch {
			var update api.UpdateCronRequest
			if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			writes++
			if update.Schedule != nil {
				cron.Schedule = *update.Schedule
			}
			// Native write committed; its successful response was lost.
			api.WriteProblem(w, api.NewProblem(502, "transport_error", "Response lost", "Retry the same request"))
			return
		}
		_ = json.NewEncoder(w).Encode(cron)
	}))
	defer backend.Close()
	cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true, Toolsets: []string{ToolsetAll}}, "2026-07-28")
	plan := callTool(t, ctx, cs, "gregale_plan_cron_change", map[string]any{"app": "alpha", "cron_id": "cron-one", "schedule": "0 * * * *", "reason": "adjust schedule"}, false)
	id := planID(t, plan)
	first, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_operation", Arguments: map[string]any{"plan_id": id}})
	if err != nil || !first.IsError {
		t.Fatalf("expected lost response: %+v %v", first, err)
	}
	var failure struct {
		Recovery Recovery `json:"recovery"`
	}
	raw, _ := json.Marshal(first.StructuredContent)
	if err := json.Unmarshal(raw, &failure); err != nil {
		t.Fatal(err)
	}
	recovery := failure.Recovery
	if recovery.Action != "inspect" {
		t.Fatalf("ambiguous cron must request inspection: %+v", recovery)
	}
	receipt := callTool(t, ctx, cs, "gregale_get_plan", map[string]any{"plan_id": id}, false)
	if err := json.Unmarshal(receipt["recovery"], &recovery); err != nil || recovery.Action != "inspect" {
		t.Fatalf("receipt must request inspection: %s, %v", receipt["recovery"], err)
	}
	replay, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_operation", Arguments: map[string]any{"plan_id": id}})
	if err != nil || !replay.IsError {
		t.Fatalf("ambiguous cron cannot be replayed without a native receipt: %+v, %v", replay, err)
	}
	mu.Lock()
	committedWrites := writes
	mu.Unlock()
	if committedWrites != 1 {
		t.Fatalf("recovery repeated a committed PATCH: %d", committedWrites)
	}
	next := callTool(t, ctx, cs, "gregale_plan_cron_change", map[string]any{"app": "alpha", "cron_id": "cron-one", "schedule": "5 * * * *", "reason": "concurrent writer guard"}, false)
	nextID := planID(t, next)
	first, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_operation", Arguments: map[string]any{"plan_id": nextID}})
	if err != nil || !first.IsError {
		t.Fatal("expected another lost response")
	}
	mu.Lock()
	cron.Path = "/another-writer"
	mu.Unlock()
	conflict, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_operation", Arguments: map[string]any{"plan_id": nextID}})
	if err != nil || !conflict.IsError {
		t.Fatal("retry overwrote a concurrent writer")
	}
	raw, _ = json.Marshal(conflict.StructuredContent)
	if err := json.Unmarshal(raw, &failure); err != nil || failure.Recovery.Action != "inspect" {
		t.Fatalf("conflicting replay must request inspection: %s, %v", raw, err)
	}
	receipt = callTool(t, ctx, cs, "gregale_get_plan", map[string]any{"plan_id": nextID}, false)
	if err := json.Unmarshal(receipt["recovery"], &recovery); err != nil || recovery.Action != "inspect" {
		t.Fatalf("conflicting receipt must request inspection: %s, %v", receipt["recovery"], err)
	}
	mu.Lock()
	defer mu.Unlock()
	if writes != 2 || cron.Path != "/another-writer" {
		t.Fatal("retry issued another PATCH")
	}
}

func TestMCPRegressionInlineSourceFitsAdvertisedLimit(t *testing.T) {
	f := newLocalFixture(t, true, "release-base")
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	uploads, err := newUploadStore(filepath.Join(t.TempDir(), "uploads"), "https://example.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, Toolsets: []string{ToolsetAll}, LocalCLI: f.cli, uploads: uploads, agentKey: true, grantID: "one"}
	source := "# " + strings.Repeat("\"", 80<<10) + "\nprint('hello')\n"
	if len(source) > maxInlineBytes {
		t.Fatal("probe exceeds advertised raw byte limit")
	}
	args := map[string]any{"app": "alpha", "reason": "inline Python source", "files": []map[string]any{{"path": "main.py", "content": source}}}
	// The source is valid according to the same planner through in-memory MCP.
	memory, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, memory, "gregale_plan_upload_deploy", args, false)
	server, err := NewServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := httptest.NewServer(newWatchHTTPHandler(func(*http.Request) *mcp.Server { return server }))
	defer endpoint.Close()
	httpCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "inline-probe", Version: "1"}, nil)
	session, err := client.Connect(httpCtx, &mcp.StreamableClientTransport{Endpoint: endpoint.URL, DisableStandaloneSSE: true}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(httpCtx, &mcp.CallToolParams{Name: "gregale_plan_upload_deploy", Arguments: args})
	if err != nil {
		t.Fatalf("valid %d-byte inline source rejected by HTTP transport: %v", len(source), err)
	}
	if result.IsError {
		t.Fatalf("valid inline source rejected: %+v", result.StructuredContent)
	}
}
