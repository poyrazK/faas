package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDiagnosticGuidanceMatchesSelectedCatalog(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/alpha":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
		case "/v1/apps/alpha/errors/summary":
			_ = json.NewEncoder(w).Encode(errorGroupsFixture("app-1", "", 1, false))
		default:
			t.Errorf("unexpected native request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer backend.Close()
	for _, tc := range []struct {
		name                   string
		sets                   []string
		needCore, needDiagnose bool
	}{
		{"core", []string{ToolsetCore}, false, true},
		{"diagnose", []string{ToolsetDiagnose}, true, false},
		{"both", []string{ToolsetCore, ToolsetDiagnose}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Toolsets: tc.sets}, "2026-07-28")
			prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_performance_diagnosis", Arguments: map[string]string{"app": "alpha"}})
			if err != nil || len(prompt.Messages) != 1 {
				t.Fatalf("prompt: %v", err)
			}
			content, ok := prompt.Messages[0].Content.(*mcp.TextContent)
			if !ok || strings.Contains(content.Text, "gregale_list_error_groups requires the core toolset") != tc.needCore || strings.Contains(content.Text, "does not list the diagnose tools") != tc.needDiagnose {
				t.Fatal("prompt omitted or misstated catalog availability")
			}
			if !tc.needCore {
				out := callTool(t, ctx, cs, "gregale_list_error_groups", errorGroupsArgs{App: "alpha", Since: errorSince, Until: errorUntil, Limit: 1}, false)
				var limitations string
				if json.Unmarshal(out["limitations"], &limitations) != nil || strings.Contains(limitations, "Request drill-down requires a connection with the diagnose toolset") != tc.needDiagnose {
					t.Fatalf("limitations=%s", out["limitations"])
				}
			}
		})
	}
}
