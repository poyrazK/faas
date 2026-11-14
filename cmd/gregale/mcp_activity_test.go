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

// TestMCPConnectionActivity checks `gregale mcp connection activity`
// (ADR-20261107220000): the id, --self and --org forms reach their routes,
// the page cursor and limit pass through, and the rows print.
func TestMCPConnectionActivity(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		wantPath string
		wantCode int
	}{
		{name: "by id", args: []string{"mcp", "connection", "activity", "key-1", "--cursor", "c1", "--limit", "5"}, wantPath: "/v1/agent-connections/key-1/activity?before=c1&limit=5"},
		{name: "self", args: []string{"mcp", "connection", "activity", "--self"}, wantPath: "/v1/agent-connections/self/activity"},
		{name: "org", args: []string{"mcp", "connection", "activity", "key-1", "--org", "acme"}, wantPath: "/v1/orgs/acme/agent-connections/key-1/activity"},
		{name: "self with id", args: []string{"mcp", "connection", "activity", "key-1", "--self"}, wantCode: 1},
		{name: "no id", args: []string{"mcp", "connection", "activity"}, wantCode: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			main.SetupCLIRegression(t)
			var mu sync.Mutex
			var got string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				got = r.URL.RequestURI()
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(api.AgentActivityResponse{
					Activity:   []api.AgentActivityEntry{{ID: "a1", Method: "DELETE", Path: "/v1/apps/demo", Status: 204, ApprovalID: "ap-1", CreatedAt: "2026-11-08T10:00:00Z"}},
					NextBefore: "next-cursor",
				})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
			var buf bytes.Buffer
			old := *main.OsStdout
			*main.OsStdout = &buf
			code := main.Run(tt.args)
			*main.OsStdout = old
			out := buf.String()
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d; out:\n%s", code, tt.wantCode, out)
			}
			if tt.wantCode != 0 {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if got != tt.wantPath {
				t.Fatalf("request = %q, want %q", got, tt.wantPath)
			}
			for _, want := range []string{"DELETE", "/v1/apps/demo", "204", "ap-1", "next-cursor"} {
				if !strings.Contains(out, want) {
					t.Fatalf("output missing %q:\n%s", want, out)
				}
			}
		})
	}
}
