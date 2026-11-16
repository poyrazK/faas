package main_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestMCPConnectionUndo checks `gregale mcp connection undo`
// (ADR-20261112080601): it plans, prints the plan, and applies it with the
// plan's digest once confirmed; --plan stops after the plan, --org uses the
// organization's routes, and --since and --entry choose the changes.
func TestMCPConnectionUndo(t *testing.T) {
	plan := api.AgentUndoPlan{Since: "2026-11-12T06:00:00Z", Undoable: 1, PlanDigest: "d1", Items: []api.AgentUndoItem{
		{ActivityID: "act-2", Summary: "Set secret TOKEN on app web", Status: api.AgentUndoNotUndoable, Reason: "secrets are not kept", CreatedAt: "2026-11-12T08:00:00Z"},
		{ActivityID: "act-1", Summary: "Changed settings of app web: min_instances", Status: api.AgentUndoUndoable, CreatedAt: "2026-11-12T07:00:00Z"},
	}}
	result := api.AgentUndoResult{Undone: 1, Items: []api.AgentUndoItem{plan.Items[0], {ActivityID: "act-1", Summary: plan.Items[1].Summary, Status: api.AgentUndoUndone, Result: api.AgentUndoResultUndone}}}
	for _, tt := range []struct {
		name      string
		args      []string
		wantCalls []string
		wantOut   []string
		wantCode  int
	}{
		{name: "plan only", args: []string{"mcp", "connection", "undo", "key-1", "--plan"},
			wantCalls: []string{`POST /v1/agent-connections/key-1/undo/plan {}`},
			wantOut:   []string{"cannot undo", "will undo", "min_instances", "1 of 2 can be undone"}},
		{name: "apply", args: []string{"mcp", "connection", "undo", "key-1", "--yes"},
			wantCalls: []string{`POST /v1/agent-connections/key-1/undo/plan {}`, `POST /v1/agent-connections/key-1/undo {"plan_digest":"d1"}`},
			wantOut:   []string{"undone", "Undid 1 change(s); 0 failed."}},
		{name: "entries and force", args: []string{"mcp", "connection", "undo", "key-1", "--entry", "act-1", "--entry", "act-3", "--force", "--plan"},
			wantCalls: []string{`POST /v1/agent-connections/key-1/undo/plan {"entries":["act-1","act-3"],"force":true}`}},
		{name: "org", args: []string{"mcp", "connection", "undo", "key-1", "--org", "acme", "--plan"},
			wantCalls: []string{`POST /v1/orgs/acme/agent-connections/key-1/undo/plan {}`}},
		{name: "since and entry", args: []string{"mcp", "connection", "undo", "key-1", "--since", "2h", "--entry", "act-1"}, wantCode: 1},
		{name: "bad since", args: []string{"mcp", "connection", "undo", "key-1", "--since", "yesterday"}, wantCode: 1},
		{name: "no id", args: []string{"mcp", "connection", "undo"}, wantCode: 1},
		{name: "unconfirmed", args: []string{"mcp", "connection", "undo", "key-1"}, wantCode: 1,
			wantCalls: []string{`POST /v1/agent-connections/key-1/undo/plan {}`}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			main.SetupCLIRegression(t)
			var mu sync.Mutex
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				calls = append(calls, r.Method+" "+r.URL.RequestURI()+" "+strings.TrimSpace(string(raw)))
				mu.Unlock()
				if strings.HasSuffix(r.URL.Path, "/plan") {
					_ = json.NewEncoder(w).Encode(plan)
					return
				}
				_ = json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
			var buf bytes.Buffer
			old := *main.OsStdout
			*main.OsStdout = &buf
			code := main.Run(tt.args)
			*main.OsStdout = old
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d; out:\n%s", code, tt.wantCode, buf.String())
			}
			mu.Lock()
			defer mu.Unlock()
			if strings.Join(calls, "\n") != strings.Join(tt.wantCalls, "\n") {
				t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(tt.wantCalls, "\n"))
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(buf.String(), want) {
					t.Fatalf("output missing %q:\n%s", want, buf.String())
				}
			}
		})
	}
}
