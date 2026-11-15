package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPRegressionCronUpdateLostResponse(t *testing.T) {
	f := newGuardedCronFixture(t)
	cs, ctx := connectConfiguredTestServer(t, f.backend, Options{APIBase: f.backend.URL, AllowWrites: true, Toolsets: []string{ToolsetAll}}, "2026-07-28")
	plan := callTool(t, ctx, cs, "gregale_plan_cron_change", map[string]any{"app": "alpha", "cron_id": f.cron.ID, "schedule": "0 * * * *", "reason": "adjust schedule"}, false)
	id := planID(t, plan)
	f.mu.Lock()
	f.lost = true
	f.mu.Unlock()
	first, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_operation", Arguments: map[string]any{"plan_id": id}})
	if err != nil || !first.IsError {
		t.Fatalf("expected lost response: %+v %v", first, err)
	}
	var failure struct {
		Recovery Recovery `json:"recovery"`
	}
	raw, _ := json.Marshal(first.StructuredContent)
	if err := json.Unmarshal(raw, &failure); err != nil || failure.Recovery.Action != "retry_same_plan" {
		t.Fatalf("guarded recovery: %s %v", raw, err)
	}
	receipt := callTool(t, ctx, cs, "gregale_get_plan", map[string]any{"plan_id": id}, false)
	var recovery Recovery
	if err := json.Unmarshal(receipt["recovery"], &recovery); err != nil || recovery.Action != "retry_same_plan" {
		t.Fatalf("receipt recovery: %v %v", receipt, err)
	}
	// Another actor changes the cron after the original update committed.
	// Recovery returns its historical receipt without overwriting that edit.
	path := "/another-writer"
	before, err := f.store.UpdateCron(ctx, f.cron.ID, nil, &path, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := callTool(t, ctx, cs, "gregale_apply_operation", map[string]any{"plan_id": id}, false)
	var native api.CronMutationReceipt
	if json.Unmarshal(result["result"], &native) != nil || native.Status != "completed" {
		t.Fatalf("native receipt: %v", result)
	}
	current, err := f.store.CronByID(ctx, f.cron.ID)
	if err != nil || current.Path != before.Path || current.ScheduleRevision != before.ScheduleRevision {
		t.Fatalf("recovery overwrote a concurrent writer: %+v %v", current, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) != 2 || f.bodies[0] != f.bodies[1] {
		t.Fatal("recovery changed the native attempt or original payload")
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
