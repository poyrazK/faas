package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
)

type fakeAccountTools struct {
	calls     int
	tool      string
	consumer  string
	out       []byte
	err       error
	accountID string
}

func (f *fakeAccountTools) AccountTool(_ context.Context, accountID, _, consumerID, tool string) ([]byte, error) {
	f.calls++
	f.accountID, f.consumer, f.tool = accountID, consumerID, tool
	return f.out, f.err
}

// toolNames is the names in a tools/list answer's result.
func toolNames(t *testing.T, body []byte) []string {
	t.Helper()
	var msg struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	names := []string{}
	for _, tool := range msg.Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// ADR-20261125194510: the account tools are appended to a tools/list
// result answering the request, replacing app tools with their names.
func TestInjectMCPAccountTools(t *testing.T) {
	id := json.RawMessage(`7`)
	for name, tc := range map[string]struct {
		body string
		want []string // nil: left alone
	}{
		"appended":        {`{"jsonrpc":"2.0","id":7,"result":{"tools":[{"name":"search"}],"nextCursor":"c"}}`, []string{"search", "account_usage", "account_plans"}},
		"empty list":      {`{"jsonrpc":"2.0","id":7,"result":{"tools":[]}}`, []string{"account_usage", "account_plans"}},
		"reserved name":   {`{"jsonrpc":"2.0","id":7,"result":{"tools":[{"name":"account_usage","description":"app's"}]}}`, []string{"account_usage", "account_plans"}},
		"string id":       {`{"jsonrpc":"2.0","id":"7","result":{"tools":[]}}`, nil},
		"other id":        {`{"jsonrpc":"2.0","id":8,"result":{"tools":[]}}`, nil},
		"object id":       {`{"jsonrpc":"2.0","id":{"a":1},"result":{"tools":[]}}`, nil},
		"error":           {`{"jsonrpc":"2.0","id":7,"error":{"code":-32601,"message":"nope"}}`, nil},
		"no tools":        {`{"jsonrpc":"2.0","id":7,"result":{"prompts":[]}}`, nil},
		"not json":        {`hello`, nil},
		"batch":           {`[{"jsonrpc":"2.0","id":7,"result":{"tools":[]}}]`, nil},
		"tools not array": {`{"jsonrpc":"2.0","id":7,"result":{"tools":{}}}`, nil},
	} {
		out, ok := injectMCPAccountTools([]byte(tc.body), id)
		if ok != (tc.want != nil) {
			t.Errorf("%s: injected = %v", name, ok)
			continue
		}
		if !ok {
			continue
		}
		if got := toolNames(t, out); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: tools = %v; want %v", name, got, tc.want)
		}
		if name == "appended" && !strings.Contains(string(out), `"nextCursor":"c"`) {
			t.Errorf("appended: lost the cursor: %s", out)
		}
	}
}

// The writer edits a JSON or event-stream tools/list answer as the app
// writes it, in pieces, and passes anything else through.
func TestMCPAccountToolsWriter(t *testing.T) {
	answer := `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"search"}]}}`
	write := func(contentType, encoding string, status int, chunks ...string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		r = r.WithContext(context.WithValue(r.Context(), mcpAccountToolsListKey{}, json.RawMessage(`1`)))
		w, finish := wrapMCPAccountToolsList(rec, r)
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", "999")
		if encoding != "" {
			w.Header().Set("Content-Encoding", encoding)
		}
		w.WriteHeader(status)
		for _, c := range chunks {
			if _, err := w.Write([]byte(c)); err != nil {
				t.Fatal(err)
			}
			w.(http.Flusher).Flush()
		}
		finish()
		return rec
	}

	rec := write("application/json; charset=utf-8", "", http.StatusOK, answer[:20], answer[20:])
	if got := toolNames(t, rec.Body.Bytes()); strings.Join(got, ",") != "search,account_usage,account_plans" || rec.Header().Get("Content-Length") != "" {
		t.Fatalf("json: tools %v, content-length %q", got, rec.Header().Get("Content-Length"))
	}

	event := "event: message\nid: e1\ndata: " + answer + "\n\n"
	rec = write("text/event-stream", "", http.StatusOK, ": ping\n\n", event[:30], event[30:], "data: {\"later\":true}\n\n")
	body := rec.Body.String()
	if !strings.HasPrefix(body, ": ping\n\n") || !strings.Contains(body, "event: message\nid: e1\ndata: ") || !strings.HasSuffix(body, "data: {\"later\":true}\n\n") {
		t.Fatalf("sse: %q", body)
	}
	data := strings.TrimPrefix(strings.Split(strings.Split(body, "data: ")[1], "\n")[0], "")
	if got := toolNames(t, []byte(data)); strings.Join(got, ",") != "search,account_usage,account_plans" {
		t.Fatalf("sse tools = %v", got)
	}

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"error status": write("application/json", "", http.StatusBadRequest, answer),
		"gzip":         write("application/json", "gzip", http.StatusOK, answer),
		"html":         write("text/html", "", http.StatusOK, answer),
		"too big":      write("application/json", "", http.StatusOK, answer, strings.Repeat(" ", api.MCPAccountToolsListMaxBytes)),
	} {
		if !strings.HasPrefix(rec.Body.String(), answer) {
			t.Errorf("%s: body changed: %.80q", name, rec.Body.String())
		}
	}

	// A request without the mark is not wrapped.
	plain := httptest.NewRecorder()
	if w, _ := wrapMCPAccountToolsList(plain, httptest.NewRequest(http.MethodPost, "/mcp", nil)); w != http.ResponseWriter(plain) {
		t.Fatal("unmarked request wrapped")
	}
}

// Account tool calls are answered by the gateway before tool scopes and
// plan admission, only for consumers whose app opted in, within a
// per-consumer limit.
func TestMCPAccountToolCalls(t *testing.T) {
	p := &api.MCPResourcePolicy{Resource: "https://app.example/mcp", Scopes: []string{"mcp:read"}, ToolScopes: map[string][]string{"read": {}}}
	rule := &EdgeRuleJWTResolved{Issuer: "https://issuer.example", MCP: p}
	app := App{ID: "app-1", AccountID: "acct-1"}
	handler := func(e *fakeOAuthEnroller, tools *fakeAccountTools) *Handler {
		h := &Handler{apiMonetization: monetizationOn, jwtVerifier: &countingJWTVerifier{onVerify: func(context.Context, string, *EdgeRuleJWTResolved) (*JWTClaims, error) {
			return &JWTClaims{Subject: "u1", Exp: time.Now().Add(time.Minute), Custom: map[string]string{"scope": "mcp:read"}}, nil
		}}}
		return h.WithOAuthConsumers(e).WithMCPAccountTools(tools)
	}
	call := func(h *Handler, body string) (*httptest.ResponseRecorder, *http.Request, bool) {
		r := httptest.NewRequest("POST", "https://app.example/mcp", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer jwt")
		r.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		return w, r, h.applyMCPResourcePolicy(w, r, app, rule)
	}
	usage := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"account_usage","arguments":{}}}`

	tools := &fakeAccountTools{out: []byte(`{"units":5}`)}
	h := handler(&fakeOAuthEnroller{id: "consumer-1", active: true, accountTools: true}, tools)
	w, _, handled := call(h, usage)
	var msg struct {
		ID     int `json:"id"`
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			Structured map[string]int `json:"structuredContent"`
			IsError    bool           `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &msg); err != nil || !handled || msg.ID != 3 || msg.Result.IsError || msg.Result.Structured["units"] != 5 ||
		msg.Result.Content[0].Text != `{"units":5}` {
		t.Fatalf("handled=%v body=%s err=%v", handled, w.Body, err)
	}
	if tools.consumer != "consumer-1" || tools.tool != api.MCPAccountToolUsage || tools.accountID != "acct-1" {
		t.Fatalf("asked apid %+v", tools)
	}

	// tools/list is left to the app, marked for the tools and asked for an
	// uncompressed answer; a later page is not marked.
	_, r, handled := call(h, `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)
	if id, _ := r.Context().Value(mcpAccountToolsListKey{}).(json.RawMessage); handled || string(id) != "4" || r.Header.Get("Accept-Encoding") != "" {
		t.Fatalf("list: handled=%v id=%s encoding=%q", handled, id, r.Header.Get("Accept-Encoding"))
	}
	_, r, _ = call(h, `{"jsonrpc":"2.0","id":5,"method":"tools/list","params":{"cursor":"c"}}`)
	if r.Context().Value(mcpAccountToolsListKey{}) != nil {
		t.Fatal("later page marked")
	}

	// Past the per-consumer limit, a tool error; apid is not asked.
	for range api.MCPAccountToolCallsPerMinute {
		call(h, usage)
	}
	before := tools.calls
	if w, _, _ := call(h, usage); !strings.Contains(w.Body.String(), `"isError":true`) || tools.calls != before {
		t.Fatalf("over the limit: %s (apid calls %d -> %d)", w.Body, before, tools.calls)
	}

	for name, tc := range map[string]struct {
		err  error
		text string
	}{
		"off":         {apidgrpc.ErrAccountToolsOff, "not available"},
		"unavailable": {errors.New("down"), "could not be read"},
	} {
		h := handler(&fakeOAuthEnroller{id: "consumer-1", active: true, accountTools: true}, &fakeAccountTools{err: tc.err})
		if w, _, handled := call(h, usage); !handled || !strings.Contains(w.Body.String(), `"isError":true`) || !strings.Contains(w.Body.String(), tc.text) {
			t.Errorf("%s: handled=%v %s", name, handled, w.Body)
		}
	}

	// Without the app's opt-in the name is an ordinary tool: here the
	// app's tool scopes do not list it, so it is refused as before.
	off := &fakeAccountTools{}
	h = handler(&fakeOAuthEnroller{id: "consumer-1", active: true}, off)
	if w, r, handled := call(h, usage); !handled || w.Code != http.StatusForbidden || off.calls != 0 {
		t.Fatalf("opt-in off: handled=%v %d %s", handled, w.Code, w.Body)
	} else if r.Context().Value(mcpAccountToolsListKey{}) != nil {
		t.Fatal("opt-in off: marked")
	}
	if _, r, _ := call(h, `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`); r.Context().Value(mcpAccountToolsListKey{}) != nil {
		t.Fatal("opt-in off: list marked")
	}
}
