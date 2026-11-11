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

// TestMCPConnectionBudget checks `gregale mcp connection budget`
// (ADR-20261111114059): reading by id or --self, and setting a dollar
// amount, unlimited or the default.
func TestMCPConnectionBudget(t *testing.T) {
	monthly := int64(2500)
	for _, tt := range []struct {
		name       string
		args       []string
		wantMethod string
		wantPath   string
		wantBody   string
		wantCode   int
	}{
		{name: "read", args: []string{"mcp", "connection", "budget", "key-1"}, wantMethod: http.MethodGet, wantPath: "/v1/agent-connections/key-1/budget"},
		{name: "self", args: []string{"mcp", "connection", "budget", "--self"}, wantMethod: http.MethodGet, wantPath: "/v1/agent-connections/self/budget"},
		{name: "set dollars", args: []string{"mcp", "connection", "budget", "key-1", "--set", "12.5"}, wantMethod: http.MethodPut, wantPath: "/v1/agent-connections/key-1/budget", wantBody: `{"monthly_cents":1250}`},
		{name: "set dollar sign", args: []string{"mcp", "connection", "budget", "key-1", "--set", "$40"}, wantMethod: http.MethodPut, wantPath: "/v1/agent-connections/key-1/budget", wantBody: `{"monthly_cents":4000}`},
		{name: "unlimited", args: []string{"mcp", "connection", "budget", "key-1", "--unlimited"}, wantMethod: http.MethodPut, wantPath: "/v1/agent-connections/key-1/budget", wantBody: `{"unlimited":true}`},
		{name: "default", args: []string{"mcp", "connection", "budget", "key-1", "--default"}, wantMethod: http.MethodPut, wantPath: "/v1/agent-connections/key-1/budget", wantBody: `{"default":true}`},
		{name: "two choices", args: []string{"mcp", "connection", "budget", "key-1", "--set", "5", "--unlimited"}, wantCode: 1},
		{name: "bad amount", args: []string{"mcp", "connection", "budget", "key-1", "--set", "5.555"}, wantCode: 1},
		{name: "self cannot set", args: []string{"mcp", "connection", "budget", "--self", "--set", "5"}, wantCode: 1},
		{name: "no id", args: []string{"mcp", "connection", "budget"}, wantCode: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			main.SetupCLIRegression(t)
			var mu sync.Mutex
			var method, path, body string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				method, path, body = r.Method, r.URL.RequestURI(), strings.TrimSpace(string(raw))
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(api.AgentBudgetResponse{MonthlyCents: &monthly, Source: api.AgentBudgetSourcePlanDefault, SpentCents: 1853, Month: "2026-11"})
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
			if tt.wantCode != 0 {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if method != tt.wantMethod || path != tt.wantPath || body != tt.wantBody {
				t.Fatalf("request = %s %s %s; want %s %s %s", method, path, body, tt.wantMethod, tt.wantPath, tt.wantBody)
			}
			for _, want := range []string{"$25.00 a month", "the plan's default", "$18.53 in 2026-11"} {
				if !strings.Contains(buf.String(), want) {
					t.Fatalf("output missing %q:\n%s", want, buf.String())
				}
			}
		})
	}
}
