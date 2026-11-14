package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestMCPConnectionStatus checks `gregale mcp connection status`: one row per
// connected agent with its pending approvals, budget used, undoable changes
// and last change; the detail of one agent with its review links; a part
// that could not be read shows as ?, not as nothing.
func TestMCPConnectionStatus(t *testing.T) {
	limit := int64(2500)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/agent-connections":
			_ = json.NewEncoder(w).Encode(api.AgentConnectionsResponse{Items: []api.AgentConnectionResponse{
				{ID: "key-1", Kind: "agent_key", Label: "deploy-bot", Approvals: []string{}},
				{ID: "key-2", Kind: "agent_key", Label: "quiet-bot", Approvals: []string{}},
			}})
		case r.URL.Path == "/v1/agent-approvals":
			_ = json.NewEncoder(w).Encode(api.AgentApprovalsResponse{Items: []api.AgentApprovalResponse{
				{ID: "ap-1", ConnectionID: "key-1", Category: "budget", Method: "PATCH", Path: "/v1/apps/shop", Status: api.AgentApprovalPending, ReviewURL: "https://gregale.dev/approvals/ap-1"},
				{ID: "ap-2", ConnectionID: "key-1", Category: "deletes", Method: "DELETE", Path: "/v1/apps/old", Status: api.AgentApprovalDenied},
			}})
		case r.URL.Path == "/v1/agent-connections/key-1/activity":
			_ = json.NewEncoder(w).Encode(api.AgentActivityResponse{Activity: []api.AgentActivityEntry{{ID: "a1", Method: "PATCH", Path: "/v1/apps/shop", Status: 200, CreatedAt: "2026-11-08T10:00:00Z"}}})
		case r.URL.Path == "/v1/agent-connections/key-2/activity":
			_ = json.NewEncoder(w).Encode(api.AgentActivityResponse{Activity: []api.AgentActivityEntry{}})
		case r.URL.Path == "/v1/agent-connections/key-1/budget":
			_ = json.NewEncoder(w).Encode(api.AgentBudgetResponse{MonthlyCents: &limit, SpentCents: 740, Month: "2026-11", Source: api.AgentBudgetSourceKey})
		case r.URL.Path == "/v1/agent-connections/key-2/budget":
			api.WriteProblem(w, api.ErrCapacity("budgets unavailable"))
		case strings.HasSuffix(r.URL.Path, "/undo/plan"):
			var req api.AgentUndoRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.Since == "" {
				t.Errorf("undo plan without a window: %+v", req)
			}
			n := 0
			if strings.Contains(r.URL.Path, "key-1") {
				n = 3
			}
			_ = json.NewEncoder(w).Encode(api.AgentUndoPlan{Undoable: n, Items: []api.AgentUndoItem{}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	run := func(args ...string) (int, string) {
		main.SetupCLIRegression(t)
		t.Setenv("FAAS_API", server.URL)
		t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
		var buf bytes.Buffer
		old := *main.OsStdout
		*main.OsStdout = &buf
		code := main.Run(args)
		*main.OsStdout = old
		return code, buf.String()
	}

	code, out := run("mcp", "connection", "status")
	if code != 0 {
		t.Fatalf("status = %d:\n%s", code, out)
	}
	for _, want := range []string{"deploy-bot", "$7.40 / $25.00", "PATCH /v1/apps/shop", "quiet-bot", "none in 7 days", "? = could not be read"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q:\n%s", want, out)
		}
	}
	var deployRow, quietRow string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "key-1"):
			deployRow = line
		case strings.HasPrefix(line, "key-2"):
			quietRow = line
		}
	}
	// key-1: one pending approval (the denied one is not), three undoable.
	if f := strings.Fields(deployRow); len(f) < 7 || f[2] != "1" || f[6] != "3" {
		t.Fatalf("deploy-bot row = %q", deployRow)
	}
	if !strings.Contains(quietRow, "?") {
		t.Fatalf("quiet-bot row hides its unreadable budget: %q", quietRow)
	}

	code, out = run("mcp", "connection", "status", "key-1")
	if code != 0 {
		t.Fatalf("detail = %d:\n%s", code, out)
	}
	for _, want := range []string{"deploy-bot (key-1)", "1 approval request(s)", "budget  PATCH /v1/apps/shop  https://gregale.dev/approvals/ap-1", "3 change(s)", "gregale mcp connection undo key-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("detail missing %q:\n%s", want, out)
		}
	}

	if code, out = run("mcp", "connection", "status", "nope"); code == 0 {
		t.Fatalf("an unknown id succeeded:\n%s", out)
	}
	code, out = run("mcp", "connection", "status", "--json")
	var parsed struct {
		Items []struct {
			Connection  api.AgentConnectionResponse `json:"connection"`
			Undoable    *int                        `json:"undoable"`
			Unavailable []string                    `json:"unavailable"`
		} `json:"items"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &parsed) != nil || len(parsed.Items) != 2 || parsed.Items[1].Unavailable[0] != "budget" {
		t.Fatalf("--json = %d %s", code, out)
	}
}
