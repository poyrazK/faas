package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeCapabilityStore struct {
	mu        sync.Mutex
	caps      AgentRunCapabilities
	running   bool
	err       error
	calls     int
	budgets   []int
	decisions []string
}

func (f *fakeCapabilityStore) AgentRunCapabilities(context.Context, AgentRunIdentity) (AgentRunCapabilities, bool, error) {
	return f.caps, f.running, f.err
}

func (f *fakeCapabilityStore) ReserveToolCall(_ context.Context, _ AgentRunIdentity, budget int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.budgets = append(f.budgets, budget)
	if f.calls >= budget {
		return false, nil
	}
	f.calls++
	return true, nil
}

func (f *fakeCapabilityStore) RecordDecision(_ context.Context, _ AgentRunIdentity, tool, decision string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decisions = append(f.decisions, tool+":"+decision)
	return nil
}

type fakeTool struct {
	name    string
	enabled bool
	err     error
	got     json.RawMessage
}

func (t *fakeTool) Name() string                { return t.name }
func (t *fakeTool) Description() string         { return "A test tool." }
func (t *fakeTool) Enabled() bool               { return t.enabled }
func (t *fakeTool) InputSchema() map[string]any { return map[string]any{"type": "object"} }
func (t *fakeTool) Call(_ context.Context, run AgentRunIdentity, args json.RawMessage) (string, bool, error) {
	t.got = args
	return "ran for " + run.RunID, false, t.err
}

var capabilityTestRun = AgentRunIdentity{AccountID: "a", RunID: "run-1", AgentID: "ag", AgentVersion: 1}

func allow(tools ...string) *api.AgentCapabilityPolicy {
	p := &api.AgentCapabilityPolicy{}
	for _, t := range tools {
		p.Tools = append(p.Tools, api.AgentCapabilityTool{Name: t, Mode: api.AgentCapabilityAllow})
	}
	return p
}

// serveCapabilities serves the gateway as an agent run's relay would.
func serveCapabilities(t *testing.T, store AgentCapabilityStore, tools ...CapabilityTool) *httptest.Server {
	t.Helper()
	metrics, err := NewCapabilityMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewCapabilityGateway(store, tools, metrics)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Capabilities: g}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run := capabilityTestRun
		h.ServeHTTP(w, r.WithContext(withRelayPrincipal(r.Context(), relayPrincipal{agentRun: &run})))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func connect(t *testing.T, srv *httptest.Server) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "harness", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + CapabilityMCPPath,
		MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func listed(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func callText(t *testing.T, cs *mcp.ClientSession, name string) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"url": "https://example.com"}})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var text []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text = append(text, tc.Text)
		}
	}
	return strings.Join(text, "\n"), res.IsError
}

// adr: 20261027090016 amendment 1 — an agent run sees only the tools its
// version allows and the operator switched on; each call counts against the
// run's tool-call budget before it runs; a refused call is recorded.
func TestCapabilityGatewayListsAndCallsAllowedTools(t *testing.T) {
	store := &fakeCapabilityStore{running: true, caps: AgentRunCapabilities{Policy: allow("web_fetch", "off_tool"), Plan: api.PlanPro}}
	fetch := &fakeTool{name: "web_fetch", enabled: true}
	off := &fakeTool{name: "off_tool"}
	denied := &fakeTool{name: "denied_tool", enabled: true}
	cs := connect(t, serveCapabilities(t, store, fetch, off, denied))

	if got := listed(t, cs); !slices.Equal(got, []string{"web_fetch"}) {
		t.Fatalf("listed %v, want only web_fetch: off_tool is switched off and denied_tool is not allowed", got)
	}
	if text, isErr := callText(t, cs, "web_fetch"); isErr || text != "ran for run-1" {
		t.Fatalf("web_fetch: %q error=%v", text, isErr)
	}
	if string(fetch.got) != `{"url":"https://example.com"}` {
		t.Errorf("tool got arguments %s", fetch.got)
	}
	if want := api.AgentToolCallsPerRunMax[api.PlanPro.PlanIndex()]; !slices.Equal(store.budgets, []int{want}) {
		t.Errorf("budgets reserved %v, want the plan's %d", store.budgets, want)
	}
	for _, tc := range []struct{ tool, decision string }{{"denied_tool", CapabilityDenied}, {"off_tool", CapabilityDisabled}} {
		text, isErr := callText(t, cs, tc.tool)
		if !isErr || text == "" {
			t.Errorf("%s: %q error=%v, want a refusal", tc.tool, text, isErr)
		}
	}
	if _, isErr := callText(t, cs, "no_such_tool"); !isErr {
		t.Error("an unknown tool was not refused")
	}
	if want := []string{"denied_tool:denied", "off_tool:disabled"}; !slices.Equal(store.decisions, want) {
		t.Errorf("decisions %v, want %v (an unknown name writes no row)", store.decisions, want)
	}
	if store.calls != 1 {
		t.Errorf("refused calls were counted against the budget: %d", store.calls)
	}
}

func TestCapabilityGatewayBudget(t *testing.T) {
	store := &fakeCapabilityStore{running: true, caps: AgentRunCapabilities{Plan: api.PlanScale,
		Policy: &api.AgentCapabilityPolicy{Tools: allow("web_fetch").Tools, Budgets: &api.AgentCapabilityBudgets{ToolCalls: 2}}}}
	cs := connect(t, serveCapabilities(t, store, &fakeTool{name: "web_fetch", enabled: true}))
	for i := range 2 {
		if _, isErr := callText(t, cs, "web_fetch"); isErr {
			t.Fatalf("call %d refused under a budget of 2", i+1)
		}
	}
	text, isErr := callText(t, cs, "web_fetch")
	if !isErr || !strings.Contains(text, "2 tool calls") {
		t.Fatalf("third call: %q error=%v, want the budget refusal", text, isErr)
	}
	if !slices.Equal(store.decisions, []string{"web_fetch:budget"}) {
		t.Errorf("decisions %v", store.decisions)
	}
}

func TestCapabilityGatewayToolFailure(t *testing.T) {
	store := &fakeCapabilityStore{running: true, caps: AgentRunCapabilities{Policy: allow("web_fetch"), Plan: api.PlanPro}}
	cs := connect(t, serveCapabilities(t, store, &fakeTool{name: "web_fetch", enabled: true, err: errors.New("dial: boom")}))
	text, isErr := callText(t, cs, "web_fetch")
	if !isErr || strings.Contains(text, "boom") {
		t.Fatalf("a gateway fault: %q error=%v, want an error result without internals", text, isErr)
	}
}

func TestCapabilityGatewayNoPolicyListsNothing(t *testing.T) {
	store := &fakeCapabilityStore{running: true, caps: AgentRunCapabilities{Plan: api.PlanPro}}
	cs := connect(t, serveCapabilities(t, store, &fakeTool{name: "web_fetch", enabled: true}))
	if got := listed(t, cs); len(got) != 0 {
		t.Fatalf("a version without a policy lists %v", got)
	}
}

// Only a running agent run reaches the gateway: another caller sees no such
// path, and a run off its lease is refused before any tool is listed.
func TestCapabilityGatewayAuthorization(t *testing.T) {
	g, err := NewCapabilityGateway(&fakeCapabilityStore{running: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	(&Handler{Capabilities: g}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, CapabilityMCPPath, strings.NewReader("{}")))
	if rec.Code != http.StatusNotFound {
		t.Errorf("an app relay or plain caller got %d, want 404", rec.Code)
	}
	for _, tc := range []struct {
		store *fakeCapabilityStore
		want  int
	}{
		{&fakeCapabilityStore{running: false}, http.StatusForbidden},
		{&fakeCapabilityStore{err: errors.New("db down")}, http.StatusServiceUnavailable},
	} {
		srv := serveCapabilities(t, tc.store)
		resp, err := http.Post(srv.URL+CapabilityMCPPath, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("store %+v: status %d, want %d", tc.store, resp.StatusCode, tc.want)
		}
	}
}
