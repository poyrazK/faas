package mcphosting

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
	// adr: 426 — POST metadata, JSON/SSE, origin validation and unbuffered progress.
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
		if request.Method == "server/discover" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Faas-Wake", "restored")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":["%s"],"capabilities":{"tools":{}}}}`, request.ID, ProtocolVersion)
			return
		}
		if request.Method == "tools/list" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Faas-Wake", "restored")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"stream","inputSchema":{"type":"object"}}]}}`, request.ID)
			return
		}
		calls++
		if request.Method == "tools/call" && r.Header.Get("Mcp-Name") != "stream" {
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
	// adr: 426 — diagnostics require a complete result and never resume work.
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

func TestCallInteractiveResumesOnlyAfterAnInputRequiredResult(t *testing.T) {
	calls := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int `json:"id"`
			Params struct {
				Name           string                   `json:"name"`
				Arguments      map[string]any           `json:"arguments"`
				InputResponses map[string]InputResponse `json:"inputResponses"`
				RequestState   string                   `json:"requestState"`
				Meta           map[string]any           `json:"_meta"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			capabilities, _ := request.Params.Meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any)
			if form, ok := capabilities["elicitation"].(map[string]any); !ok || form["form"] == nil {
				t.Errorf("form elicitation capability missing: %v", capabilities)
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"input_required","inputRequests":{"dates":{"method":"elicitation/create","params":{"mode":"form","message":"Choose dates","requestedSchema":{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"}},"required":["from","to"]}}}},"requestState":"opaque-state"}}`, request.ID)
			return
		}
		if calls != 2 || request.Params.Name != "report_preview" || request.Params.Arguments["report"] != "sales" || request.Params.RequestState != "opaque-state" {
			t.Errorf("continuation changed original request: %+v", request.Params)
		}
		response, ok := request.Params.InputResponses["dates"]
		if !ok || response.Action != "accept" || response.Content["from"] != "2026-06-01" || response.Content["to"] != "2026-06-30" {
			t.Errorf("continuation response=%+v", request.Params.InputResponses)
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","content":[{"type":"text","text":"done"}]}}`, request.ID)
	})
	tool := Tool{Name: "report_preview", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"report": map[string]any{"type": "string"}}}}
	var prompted int
	exchange, err := client.CallInteractive(context.Background(), tool, map[string]any{"report": "sales"}, false, func(_ context.Context, form InputRequest) (InputResponse, error) {
		prompted++
		if form.Tool != "report_preview" || form.ID != "dates" || form.Message != "Choose dates" || form.Schema["type"] != "object" {
			t.Errorf("unexpected form: %+v", form)
		}
		return InputResponse{Action: "accept", Content: map[string]any{"from": "2026-06-01", "to": "2026-06-30"}}, nil
	})
	if err != nil || calls != 2 || prompted != 1 || !strings.Contains(string(exchange.Result), "done") {
		t.Fatalf("exchange=%+v err=%v calls=%d prompted=%d", exchange, err, calls, prompted)
	}
}

func TestCallInteractiveCanResumeWithoutInputRequestsOrRequestState(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int            `json:"id"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		count := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"input_required"}}`, request.ID)
			return
		}
		if _, ok := request.Params["requestState"]; ok {
			t.Errorf("sent absent request state: %v", request.Params)
		}
		if _, ok := request.Params["inputResponses"]; ok {
			t.Errorf("sent responses without input requests: %v", request.Params)
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","content":[]}}`, request.ID)
	})
	tool := Tool{Name: "tool", InputSchema: map[string]any{"type": "object"}}
	_, err := client.CallInteractive(context.Background(), tool, nil, false, func(context.Context, InputRequest) (InputResponse, error) {
		t.Fatal("unexpected prompt with no input requests")
		return InputResponse{}, nil
	})
	if err != nil || calls.Load() != 2 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestCallInteractiveDeclineIsSentWithoutFormContent(t *testing.T) {
	calls := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int `json:"id"`
			Params struct {
				InputResponses map[string]InputResponse `json:"inputResponses"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"input_required","inputRequests":{"confirm":{"method":"elicitation/create","params":{"mode":"form","requestedSchema":{"type":"object","properties":{"ok":{"type":"boolean"}}}}}},"requestState":"opaque"}}`, request.ID)
			return
		}
		response := request.Params.InputResponses["confirm"]
		if response.Action != "decline" || response.Content != nil {
			t.Errorf("decline response=%+v", response)
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","content":[{"type":"text","text":"cancelled"}]}}`, request.ID)
	})
	tool := Tool{Name: "confirm", InputSchema: map[string]any{"type": "object"}}
	exchange, err := client.CallInteractive(context.Background(), tool, nil, false, func(context.Context, InputRequest) (InputResponse, error) {
		return InputResponse{Action: "decline"}, nil
	})
	if err != nil || calls != 2 || !strings.Contains(string(exchange.Result), "cancelled") {
		t.Fatalf("exchange=%+v err=%v calls=%d", exchange, err, calls)
	}
}

func TestCallInteractiveSuppressesPartialStateOnResponderError(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"input_required","requestState":"private-continuation-state","inputRequests":{"confirm":{"method":"elicitation/create","params":{"mode":"form","requestedSchema":{"type":"object","properties":{"ok":{"type":"boolean"}}}}}}}}`, request.ID)
	})
	tool := Tool{Name: "confirm", InputSchema: map[string]any{"type": "object"}}
	exchange, err := client.CallInteractive(context.Background(), tool, nil, false, func(context.Context, InputRequest) (InputResponse, error) {
		return InputResponse{}, fmt.Errorf("response unavailable")
	})
	if err == nil || calls.Load() != 1 || len(exchange.Result) != 0 {
		t.Fatalf("exchange=%+v err=%v calls=%d", exchange, err, calls.Load())
	}
}

func TestCallInteractiveBoundsInputRounds(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"input_required","requestState":"state","inputRequests":{"value":{"method":"elicitation/create","params":{"mode":"form","requestedSchema":{"type":"object","properties":{"value":{"type":"string"}}}}}}}}`, request.ID)
	})
	tool := Tool{Name: "repeat", InputSchema: map[string]any{"type": "object"}}
	var prompts atomic.Int32
	_, err := client.CallInteractive(context.Background(), tool, nil, false, func(context.Context, InputRequest) (InputResponse, error) {
		prompts.Add(1)
		return InputResponse{Action: "accept", Content: map[string]any{"value": "again"}}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "round limit") || calls.Load() != maxInteractiveInputRounds+1 || prompts.Load() != maxInteractiveInputRounds {
		t.Fatalf("err=%v calls=%d prompts=%d", err, calls.Load(), prompts.Load())
	}
}

func TestCallInteractiveRejectsLegacyAndNilResponders(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) { t.Fatal("unexpected request") })
	client.Version = LegacyProtocolVersion
	tool := Tool{Name: "tool", InputSchema: map[string]any{}}
	if _, err := client.CallInteractive(context.Background(), tool, nil, false, func(context.Context, InputRequest) (InputResponse, error) { return InputResponse{}, nil }); err == nil || !strings.Contains(err.Error(), "requires protocol") {
		t.Fatalf("legacy call error=%v", err)
	}
	client.Version = ProtocolVersion
	if _, err := client.CallInteractive(context.Background(), tool, nil, false, nil); err == nil || !strings.Contains(err.Error(), "response handler") {
		t.Fatalf("nil responder error=%v", err)
	}
}

func TestCallRejectsSessionCreatedDuringExecution(t *testing.T) {
	// adr: 426 — stateless qualification applies to tool execution as well as discovery.
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

func TestClientDiscoveryRejectsIndividualToolsAcrossPages(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int `json:"id"`
			Params struct {
				Cursor string `json:"cursor"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if request.Params.Cursor == "next" {
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"bad2","inputSchema":{"properties":{"n":{"type":"number","x-mcp-header":"N"}}}},{"name":"good2","inputSchema":{"type":"object"}}]}}`, request.ID)
			return
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"bad1","inputSchema":{"properties":{"x":{"type":"string","x-mcp-header":"bad name"}}}},{"name":"good1","inputSchema":{"type":"object"}}],"nextCursor":"next"}}`, request.ID)
	})
	tools, discovery, err := c.Tools(context.Background())
	if err != nil || len(tools) != 2 || tools[0].Name != "good1" || tools[1].Name != "good2" {
		t.Fatalf("tools=%+v err=%v", tools, err)
	}
	if len(discovery.RejectedTools) != 2 || discovery.RejectedTools[0].Name != "bad1" || discovery.RejectedTools[1].Name != "bad2" || discovery.RejectedTools[0].Reason == "" {
		t.Fatalf("rejections=%+v", discovery.RejectedTools)
	}
}

func TestClientDiscoveryRetainsDuplicateDefenseAfterRejection(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"same","inputSchema":null},{"name":"same","inputSchema":{"type":"object"}}]}}`)
	})
	if _, _, err := c.Tools(context.Background()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate accepted after rejection: %v", err)
	}
}

func TestResourceAndPromptCatalogsAreExplicitAndPaginated(t *testing.T) {
	methods := make([]string, 0)
	readCalled, promptCalled, toolCalled := false, false, false
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		methods = append(methods, request.Method)
		w.Header().Set("Content-Type", "application/json")
		respond := func(body string) { _, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, request.ID, body) }
		switch request.Method {
		case "server/discover":
			respond(fmt.Sprintf(`{"supportedVersions":[%q],"capabilities":{"resources":{},"prompts":{},"extensions":{"%s":{}}}}`, ProtocolVersion, TasksExtensionID))
		case "resources/list":
			if request.Params["cursor"] == "next" {
				respond(`{"resources":[{"uri":"file:///two","name":"two","size":9007199254740993}]}`)
			} else {
				respond(`{"resources":[{"uri":"file:///one","name":"one","mimeType":"text/plain"}],"nextCursor":"next"}`)
			}
		case "resources/templates/list":
			respond(`{"resourceTemplates":[{"uriTemplate":"file:///users/{id}","name":"user"}]}`)
		case "prompts/list":
			respond(`{"prompts":[{"name":"summarize","arguments":[{"name":"period","required":true}]}]}`)
		case "resources/read":
			readCalled = true
			if request.Params["uri"] != "file:///one" {
				t.Errorf("resource URI=%v", request.Params["uri"])
			}
			respond(`{"contents":[{"uri":"file:///one","text":"private-body"}]}`)
		case "prompts/get":
			promptCalled = true
			if request.Params["name"] != "summarize" {
				t.Errorf("prompt name=%v", request.Params["name"])
			}
			arguments, _ := request.Params["arguments"].(map[string]any)
			if arguments["period"] != "week" {
				t.Errorf("prompt args=%v", arguments)
			}
			respond(`{"messages":[{"role":"user","content":{"type":"text","text":"private-prompt"}}]}`)
		case "tools/list", "tools/call":
			toolCalled = true
			respond(`{"tools":[]}`)
		default:
			t.Errorf("unexpected MCP method %s", request.Method)
			respond(`{}`)
		}
	})
	catalog, _, err := c.DiscoverCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Tools) != 0 || len(catalog.Resources) != 2 || len(catalog.ResourceTemplates) != 1 || len(catalog.Prompts) != 1 || len(catalog.Extensions) != 1 || catalog.Extensions[0] != TasksExtensionID {
		t.Fatalf("catalog=%+v", catalog)
	}
	if *catalog.Resources[1].Size != 9007199254740993 {
		t.Fatalf("resource size precision lost: %+v", catalog.Resources[1])
	}
	if readCalled || promptCalled || toolCalled {
		t.Fatalf("catalog discovery performed a side effect: methods=%v", methods)
	}
	if _, err := c.ReadResource(context.Background(), "file:///one"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetPrompt(context.Background(), "summarize", map[string]string{"period": "week"}); err != nil {
		t.Fatal(err)
	}
	if !readCalled || !promptCalled || toolCalled {
		t.Fatalf("explicit requests: read=%v prompt=%v tool=%v", readCalled, promptCalled, toolCalled)
	}
}

func TestDiscoverRejectsServerWithNoCatalog(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":[%q],"capabilities":{}}}`, request.ID, ProtocolVersion)
	})
	if _, _, err := c.DiscoverCatalog(context.Background()); err == nil || !strings.Contains(err.Error(), "advertise tools, resources or prompts") {
		t.Fatalf("accepted server with no MCP catalog: %v", err)
	}
}

func TestPromptGetDoesNotExposeUncontinuedInputRequest(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"input_required","requestState":"opaque-private-state","inputRequests":{"x":{"method":"elicitation/create"}}}}`, request.ID)
	})
	x, err := c.GetPrompt(context.Background(), "needs-input", nil)
	if err == nil || !strings.Contains(err.Error(), "input_required") || x.Result != nil {
		t.Fatalf("input request was exposed: result=%s err=%v", x.Result, err)
	}
}

func TestLegacyInitializeSupportsResourceOnlyCatalog(t *testing.T) {
	methods := make([]string, 0)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		methods = append(methods, request.Method)
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "initialize":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":%q,"capabilities":{"resources":{}}}}`, request.ID, LegacyProtocolVersion)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/list":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resources":[{"uri":"file:///one","name":"one"}]}}`, request.ID)
		case "resources/templates/list":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resourceTemplates":[]}}`, request.ID)
		default:
			t.Errorf("unexpected legacy method %s", request.Method)
		}
	})
	c.Version = LegacyProtocolVersion
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	catalog, _, err := c.DiscoverCatalog(context.Background())
	if err != nil || len(catalog.Resources) != 1 || len(catalog.Tools) != 0 {
		t.Fatalf("catalog=%+v err=%v methods=%v", catalog, err, methods)
	}
	if strings.Join(methods, ",") != "initialize,notifications/initialized,resources/list,resources/templates/list" {
		t.Fatalf("unexpected compatibility negotiation: %v", methods)
	}
}
