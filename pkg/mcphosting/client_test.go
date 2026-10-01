package mcphosting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	c, err := NewClient(s.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestStatelessDiscoveryAndStreaming(t *testing.T) {
	// adr: 423 — POST metadata, JSON/SSE, origin validation and unbuffered progress.
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			w.WriteHeader(403)
			return
		}
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		meta, _ := request.Params["_meta"].(map[string]any)
		if meta["io.modelcontextprotocol/protocolVersion"] != ProtocolVersion || r.Header.Get("Mcp-Method") != request.Method {
			t.Error("modern request metadata missing")
		}
		if request.Method == "tools/list" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Faas-Wake", "restored")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"stream","inputSchema":{"type":"object"}}]}}`, request.ID)
			return
		}
		calls++
		if r.Header.Get("Mcp-Name") != "stream" {
			t.Error("missing tool header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, ": keepalive\n\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progressToken\":\"gregale-doctor\",\"progress\":1}}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(150 * time.Millisecond)
		_, _ = fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progressToken\":\"gregale-doctor\",\"progress\":2}}\n\n")
		w.(http.Flusher).Flush()
		_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":[]}}\n\n", request.ID)
	})
	report := Doctor(context.Background(), c, false, "", nil)
	if !report.OK || calls != 0 || report.Discovery.WakeTier != "restored" {
		t.Fatalf("discovery: %+v; calls=%d", report, calls)
	}
	report = Doctor(context.Background(), c, false, "stream", nil)
	if !report.OK || calls != 1 || len(report.Stream.ProgressMS) != 2 {
		t.Fatalf("stream: %+v; calls=%d", report, calls)
	}
}

func TestClientRejectsInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body, content string
		status              int
		want                string
	}{
		{"HTTP error", "", "application/json", 503, "HTTP 503"},
		{"RPC error", `{"jsonrpc":"2.0","id":1,"error":{"code":-32601}}`, "application/json", 200, "JSON-RPC error"},
		{"wrong ID", `{"jsonrpc":"2.0","id":99,"result":{}}`, "application/json", 200, "ID"},
		{"wrong version", `{"jsonrpc":"1.0","id":1,"result":{}}`, "application/json", 200, "version"},
		{"not MCP", `{"status":"ok"}`, "application/json", 200, "version"},
		{"bad content", "hello", "text/plain", 200, "JSON or SSE"},
		{"truncated SSE", "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n", "text/event-stream", 200, "final result"},
		{"stateful", `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`, "application/json", 200, "stateful"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.content)
				if tc.name == "stateful" {
					w.Header().Set("Mcp-Session-Id", "session")
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			})
			_, _, err := c.Tools(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

func TestCallDoesNotRetryToolErrors(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Mcp-Param-Customer") != "=?base64?5a6i5oi3?=" {
			t.Errorf("mirrored header %q", r.Header.Get("Mcp-Param-Customer"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[]}}`)
	})
	schema := map[string]any{"properties": map[string]any{"customer": map[string]any{"type": "string", "x-mcp-header": "Customer"}}}
	x, err := c.Call(context.Background(), Tool{Name: "charge", InputSchema: schema}, map[string]any{"customer": "客户"}, false)
	if err == nil || x.Result == nil || calls != 1 {
		t.Fatalf("x=%+v, err=%v, calls=%d", x, err, calls)
	}
}

func TestCallRejectsIncompleteResults(t *testing.T) {
	// adr: 423 — diagnostics require a complete result and never resume work.
	for _, result := range []string{`{}`, `{"content":null}`, `{"resultType":"task","content":[]}`, `{"resultType":"input_required","content":[]}`} {
		t.Run(result, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%s}`, result)
			})
			if _, err := c.Call(context.Background(), Tool{Name: "task", InputSchema: map[string]any{}}, nil, false); err == nil || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestCallRejectsSessionCreatedDuringExecution(t *testing.T) {
	// adr: 423 — stateless qualification applies to tool execution as well as discovery.
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "new-session")
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`)
	})
	if _, err := c.Call(context.Background(), Tool{Name: "tool", InputSchema: map[string]any{}}, nil, false); err == nil || !strings.Contains(err.Error(), "stateful") {
		t.Fatalf("accepted session created during tool execution: %v", err)
	}
}

func TestClientNeverFollowsCredentialRedirect(t *testing.T) {
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked = true }))
	defer target.Close()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) })
	c.Token = "client-secret"
	_, _, err := c.Tools(context.Background())
	if err == nil || leaked {
		t.Fatalf("redirect err=%v, leaked=%t", err, leaked)
	}
}

func TestLegacyStatelessHandshakeAndPagination(t *testing.T) {
	methods := []string{}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		methods = append(methods, request.Method)
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "initialize":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25"}}`, request.ID)
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/list":
			name, cursor := "first", `,"nextCursor":"next"`
			if request.Params["cursor"] == "next" {
				name, cursor = "second", ""
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":%q,"inputSchema":{}}]%s}}`, request.ID, name, cursor)
		}
	})
	c.Version = LegacyProtocolVersion
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	tools, _, err := c.Tools(context.Background())
	if err != nil || len(tools) != 2 || strings.Join(methods, ",") != "initialize,notifications/initialized,tools/list,tools/list" {
		t.Fatalf("tools=%+v err=%v methods=%v", tools, err, methods)
	}
}

func TestMirroredHeaderValidation(t *testing.T) {
	for _, schema := range []string{
		`{"items":{"type":"string","x-mcp-header":"Bad"}}`,
		`{"properties":{"x":{"type":"number","x-mcp-header":"Bad"}}}`,
		`{"properties":{"x":{"type":"string","x-mcp-header":"Bad\r\n"}}}`,
		`{"properties":{"x":{"type":"string","x-mcp-header":"Same"},"y":{"type":"boolean","x-mcp-header":"same"}}}`,
	} {
		var s map[string]any
		_ = json.Unmarshal([]byte(schema), &s)
		if _, err := parameterHeaders(s, nil); err == nil {
			t.Fatalf("accepted %s", schema)
		}
	}
	for _, value := range []float64{1.5, 9007199254740992} {
		if _, err := primitiveHeader(value, "integer"); err == nil {
			t.Fatalf("accepted unsafe integer %f", value)
		}
	}
	if encodeHeader(" padded ") != "=?base64?IHBhZGRlZCA=?=" || encodeHeader("=?base64?literal?=") == "=?base64?literal?=" {
		t.Fatal("ambiguous header encoding")
	}
}
