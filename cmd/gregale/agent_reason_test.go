package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestAgentReasonPrefix checks the --agent-reason prefix and
// GREGALE_AGENT_REASON (ADR-20261110182236): write requests carry the
// sanitised reason in Gregale-Agent-Reason, reads carry none, and the flag
// wins over the environment.
func TestAgentReasonPrefix(t *testing.T) {
	for _, tt := range []struct {
		name, env string
		args      []string
		want      string
	}{
		{name: "flag", args: []string{"--agent-reason", "route ops\nalerts", "mcp", "approval-channels", "--clear"}, want: "route ops alerts"},
		{name: "flag equals", args: []string{"--agent-reason=quiet the pager", "mcp", "approval-channels", "--clear"}, want: "quiet the pager"},
		{name: "env", env: "from the environment", args: []string{"mcp", "approval-channels", "--clear"}, want: "from the environment"},
		{name: "flag beats env", env: "env reason", args: []string{"--agent-reason", "flag reason", "mcp", "approval-channels", "--clear"}, want: "flag reason"},
		{name: "none", args: []string{"mcp", "approval-channels", "--clear"}, want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			main.SetupCLIRegression(t)
			var mu sync.Mutex
			seen := map[string]string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen[r.Method] = r.Header.Get(api.AgentReasonHeader)
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(api.AgentApprovalChannelsResponse{})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
			t.Setenv("GREGALE_AGENT_REASON", tt.env)
			var buf bytes.Buffer
			old := *main.OsStdout
			*main.OsStdout = &buf
			code := main.Run(tt.args)
			*main.OsStdout = old
			if code != 0 {
				t.Fatalf("exit = %d; out:\n%s", code, buf.String())
			}
			mu.Lock()
			defer mu.Unlock()
			got, ok := seen[http.MethodPut]
			if !ok {
				t.Fatalf("no write request reached the server: %v", seen)
			}
			if got != tt.want {
				t.Fatalf("Gregale-Agent-Reason = %q, want %q", got, tt.want)
			}
			if reason := seen[http.MethodGet]; reason != "" {
				t.Fatalf("a read carried a reason: %q", reason)
			}
		})
	}
	t.Run("missing text", func(t *testing.T) {
		main.SetupCLIRegression(t)
		if code := main.Run([]string{"--agent-reason"}); code == 0 {
			t.Fatal("--agent-reason without text succeeded")
		}
	})
}

// TestMCPConnectionActivityShowsReason prints an activity row's reason,
// quoted, under the row.
func TestMCPConnectionActivityShowsReason(t *testing.T) {
	main.SetupCLIRegression(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AgentActivityResponse{Activity: []api.AgentActivityEntry{{
			ID: "a1", Method: "DELETE", Path: "/v1/apps/demo", Status: 204, Reason: `remove the "demo" app`, CreatedAt: "2026-11-10T10:00:00Z",
		}}})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	var buf bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &buf
	code := main.Run([]string{"mcp", "connection", "activity", "--self"})
	*main.OsStdout = old
	if code != 0 {
		t.Fatalf("exit = %d; out:\n%s", code, buf.String())
	}
	if want := `reason: "remove the \"demo\" app"`; !strings.Contains(buf.String(), want) {
		t.Fatalf("output missing %s:\n%s", want, buf.String())
	}
}
