// adr: 1618
package mcpmanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const testToken = "fp_test_management_fixture_credential"

func connectTestServer(t *testing.T, backend *httptest.Server, timeout time.Duration, version string) (*mcp.ClientSession, context.Context) {
	t.Helper()
	return connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, Profile: "staging", Token: testToken, Timeout: timeout}, version)
}

func connectConfiguredTestServer(t *testing.T, backend *httptest.Server, opts Options, version string) (*mcp.ClientSession, context.Context) {
	t.Helper()
	c := api.NewClient(backend.URL, testToken)
	c.SetCompletionCache(nil)
	s, err := NewServer(c, opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "management-test", Version: "1"}, nil).Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, ctx
}

func callTool(t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args any, wantError bool) map[string]json.RawMessage {
	t.Helper()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError != wantError {
		t.Fatalf("%s error=%v, want %v; content=%v", name, result.IsError, wantError, result.Content)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), testToken) {
		t.Fatalf("%s exposed the credential", name)
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			if strings.Contains(text.Text, testToken) {
				t.Fatalf("%s text fallback exposed the credential", name)
			}
			// The text fallback preserves the actual wire integers. A client
			// that decodes StructuredContent into any may coerce them to float64.
			data = []byte(text.Text)
		}
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestManagementMCPJourney(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			var requests atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+testToken {
					t.Errorf("unexpected request method or credential: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/account":
					_, _ = w.Write([]byte(`{"id":"account-one","plan":"free","status":"active","tax_id":"private-tax","email":"private@example.com"}`))
				case "/v1/capabilities":
					_, _ = w.Write([]byte(`{"registry_version":1,"plan":"free","capabilities":[]}`))
				case "/v1/apps":
					_, _ = w.Write([]byte(`[{"id":"b","slug":"bravo","status":"active"},{"id":"a","slug":"alpha","manifest":{"env":{"PRIVATE":"must-not-return"}}}]`))
				case "/v1/apps/alpha":
					_, _ = w.Write([]byte(`{"id":"a","slug":"alpha","status":"active","deployment_availability":"serving","url":"https://alpha.gregale.dev","manifest":{"env":{"PRIVATE":"must-not-return"}}}`))
				case "/v1/deployments":
					if r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("before") != "cursor-previous" {
						t.Error("deployment pagination was not forwarded")
					}
					_, _ = w.Write([]byte(`{"items":[{"id":"deployment-one","app_id":"a","status":"failed","commit_sha":"123abc"}],"next_before":"cursor-next"}`))
				case "/v1/deployments/deployment-one":
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "deployment-one", "status": "failed", "error_code": "build_failed", "error": "reflected " + testToken, "override_cmd": []string{"private-argument"}})
				case "/v1/apps/alpha/metrics":
					if r.URL.Query().Get("range") != "1h" {
						t.Error("metrics range was not forwarded")
					}
					_, _ = w.Write([]byte(`{"app_id":"a","range":"1h","source":"degraded: unavailable","request_count":9007199254740993}`))
				case "/v1/apps/alpha/logs":
					q := r.URL.Query()
					if q.Get("follow") != "0" || q.Get("level") != "error" || q.Get("grep") != "failed" || q.Get("deployment") != "deployment-one" || q.Get("since") != "2026-10-11T12:00:00Z" {
						t.Error("log filters were not forwarded or follow was enabled")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "event: gap\ndata: {\"reason\":\"since_below_retained\",\"replay_advised\":true}\n\n")
					for i := range 3 {
						data, _ := json.Marshal(api.LogEvent{Seq: int64(i + 1), Line: "failed " + testToken, Stream: "stderr"})
						_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
					}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer backend.Close()
			cs, ctx := connectTestServer(t, backend, 0, version)
			catalog, err := cs.ListTools(ctx, nil)
			if err != nil || len(catalog.Tools) != 58 {
				t.Fatalf("catalog=%+v error=%v", catalog, err)
			}
			for _, tool := range catalog.Tools {
				if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || tool.InputSchema == nil {
					t.Errorf("tool lacks read-only annotation or schema: %s", tool.Name)
				}
			}
			if requests.Load() != 0 {
				t.Fatal("MCP discovery made an API request")
			}
			connection := callTool(t, ctx, cs, "gregale_context", map[string]any{}, false)
			if string(connection["profile"]) != `"staging"` || strings.Contains(string(connection["account"]), "private") {
				t.Fatalf("connection=%s", connection)
			}
			page := callTool(t, ctx, cs, "gregale_list_apps", map[string]any{"limit": 1}, false)
			if string(page["next_offset"]) != "1" || !strings.Contains(string(page["items"]), "alpha") || strings.Contains(string(page["items"]), "must-not-return") {
				t.Fatalf("apps=%s", page)
			}
			page = callTool(t, ctx, cs, "gregale_list_apps", map[string]any{"limit": 1, "offset": 1}, false)
			if _, more := page["next_offset"]; more || !strings.Contains(string(page["items"]), "bravo") {
				t.Fatalf("second page=%s", page)
			}
			app := callTool(t, ctx, cs, "gregale_get_app", map[string]any{"app": "alpha"}, false)
			if _, ok := app["manifest"]; ok || string(app["status"]) != `"active"` {
				t.Fatalf("app=%s", app)
			}
			page = callTool(t, ctx, cs, "gregale_list_deployments", map[string]any{"limit": 1, "before": "cursor-previous"}, false)
			if string(page["next_before"]) != `"cursor-next"` {
				t.Fatalf("deployments=%s", page)
			}
			deployment := callTool(t, ctx, cs, "gregale_get_deployment", map[string]any{"deployment_id": "deployment-one"}, false)
			if _, ok := deployment["override_cmd"]; ok || string(deployment["error_code"]) != `"build_failed"` {
				t.Fatalf("deployment=%s", deployment)
			}
			metrics := callTool(t, ctx, cs, "gregale_get_metrics", map[string]any{"app": "alpha", "range": "1h"}, false)
			if string(metrics["request_count"]) != "9007199254740993" || !strings.Contains(string(metrics["source"]), "degraded") {
				t.Fatalf("metrics=%s", metrics)
			}
			logs := callTool(t, ctx, cs, "gregale_get_logs", map[string]any{"app": "alpha", "limit": 2, "level": "error", "grep": "failed", "deployment_id": "deployment-one", "since": "2026-10-11T12:00:00Z"}, false)
			var entries []json.RawMessage
			if err := json.Unmarshal(logs["items"], &entries); err != nil || len(entries) != 2 || string(logs["truncated"]) != "true" || len(logs["gaps"]) == 0 {
				t.Fatalf("logs=%s error=%v", logs, err)
			}
			if !strings.Contains(string(entries[0]), `"seq":2`) || !strings.Contains(string(entries[1]), `"seq":3`) {
				t.Fatalf("log limit omitted the newest entries: %s", entries)
			}
		})
	}
}

func TestManagementAPIProblems(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(api.Problem{Status: status, Code: "scoped_failure", Detail: "reflected " + testToken, Hint: "Check the token " + testToken})
			}))
			defer backend.Close()
			cs, ctx := connectTestServer(t, backend, 0, "")
			out := callTool(t, ctx, cs, "gregale_get_app", map[string]any{"app": "alpha"}, true)
			if string(out["code"]) != `"scoped_failure"` || string(out["status"]) != fmt.Sprint(status) || string(out["retry_after_seconds"]) != "7" {
				t.Fatalf("problem=%s", out)
			}
		})
	}
}

func TestManagementRejectsInvalidInputsBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer backend.Close()
	cs, ctx := connectTestServer(t, backend, 0, "")
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"gregale_get_app", map[string]any{"app": "../secrets"}},
		{"gregale_get_app", map[string]any{"app": "alpha?scope=admin"}},
		{"gregale_get_deployment", map[string]any{"deployment_id": "../account"}},
		{"gregale_list_apps", map[string]any{"offset": -1}},
		{"gregale_list_apps", map[string]any{"limit": 101}},
		{"gregale_list_deployments", map[string]any{"limit": -1}},
		{"gregale_get_logs", map[string]any{"app": "alpha", "since": "yesterday"}},
		{"gregale_get_logs", map[string]any{"app": "alpha", "level": "debug"}},
		{"gregale_get_logs", map[string]any{"app": "alpha", "grep": "foo\nbar"}},
		{"gregale_get_metrics", map[string]any{"app": "../secrets"}},
	} {
		callTool(t, ctx, cs, tc.tool, tc.args, true)
	}
	for _, args := range []map[string]any{{}, {"app": 123}} {
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_get_app", Arguments: args})
		if err != nil || !result.IsError {
			t.Fatalf("schema validation result=%+v error=%v", result, err)
		}
	}
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_deploy", Arguments: map[string]any{}}); err == nil {
		t.Fatal("unregistered mutation tool was accepted")
	}
	if requests.Load() != 0 {
		t.Fatal("invalid inputs reached the API")
	}
}

func TestManagementTimeoutCancelsAPIRequest(t *testing.T) {
	cancelled := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(cancelled)
	}))
	defer backend.Close()
	cs, ctx := connectTestServer(t, backend, 50*time.Millisecond, "")
	result := callTool(t, ctx, cs, "gregale_get_app", map[string]any{"app": "alpha"}, true)
	if string(result["code"]) != `"timeout"` {
		t.Fatalf("result=%s", result)
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("API request was not cancelled")
	}
}

func TestManagementBoundsAndLogFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		metrics          bool
	}{
		{"large-result", `{"source":"` + strings.Repeat("x", maxResultBytes) + `"}`, "response_too_large", true},
		{"large-logs", strings.Repeat("x", maxLogBytes+1), "response_too_large", false},
		{"malformed-entry", "event: log\ndata: {bad}\n\n", "invalid_response", false},
		{"stream-error", "event: error\ndata: {\"code\":\"logs_denied\",\"detail\":\"not permitted\"}\n\n", "logs_denied", false},
		{"degraded", "event: degraded\ndata: {}\n\n", "logs_unavailable", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, tc.body) }))
			defer backend.Close()
			cs, ctx := connectTestServer(t, backend, 0, "")
			tool := "gregale_get_logs"
			if tc.metrics {
				tool = "gregale_get_metrics"
			}
			out := callTool(t, ctx, cs, tool, map[string]any{"app": "alpha"}, true)
			if string(out["code"]) != fmt.Sprintf("%q", tc.code) {
				t.Fatalf("result=%s", out)
			}
		})
	}
}

func TestLogRedactionPrecedesUTF8Truncation(t *testing.T) {
	line := strings.Repeat("é", 500) + testToken + strings.Repeat("界", 100)
	data, _ := json.Marshal(api.LogEvent{Line: line})
	m := &management{opts: Options{Token: testToken}}
	result, err := decodeLogs(context.Background(), []byte("event: log\ndata: "+string(data)+"\n\n"), 1, m.clean)
	if err != nil || !result.Truncated || !result.Items[0].LineTruncated {
		t.Fatalf("snapshot=%+v error=%v", result, err)
	}
	if strings.Contains(result.Items[0].Line, "fp_test") || !json.Valid([]byte(fmt.Sprintf("%q", result.Items[0].Line))) {
		t.Fatal("log truncation exposed a token prefix or invalid text")
	}
}
