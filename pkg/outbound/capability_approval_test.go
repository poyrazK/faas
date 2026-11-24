package outbound

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
)

// fakeApprovalStore holds approvals in memory; decide answers each read.
type fakeApprovalStore struct {
	fakeCapabilityStore
	amu      sync.Mutex
	refuse   bool
	expires  time.Time
	requests []ToolApprovalRequest
	reads    int
	released []string
	decide   func(reads int) string
}

func (f *fakeApprovalStore) RequestApproval(_ context.Context, _ AgentRunIdentity, req ToolApprovalRequest) (ToolApproval, error) {
	f.amu.Lock()
	defer f.amu.Unlock()
	if f.refuse {
		return ToolApproval{}, ErrApprovalRefused
	}
	f.requests = append(f.requests, req)
	return ToolApproval{ID: "ap-1", ExpiresAt: f.expires}, nil
}

func (f *fakeApprovalStore) ApprovalDecision(context.Context, AgentRunIdentity, string) (string, error) {
	f.amu.Lock()
	defer f.amu.Unlock()
	f.reads++
	return f.decide(f.reads), nil
}

func (f *fakeApprovalStore) ReleaseApproval(_ context.Context, _ AgentRunIdentity, id string) error {
	f.amu.Lock()
	defer f.amu.Unlock()
	f.released = append(f.released, id)
	return nil
}

func approve(tools ...string) *api.AgentCapabilityPolicy {
	p := &api.AgentCapabilityPolicy{}
	for _, t := range tools {
		p.Tools = append(p.Tools, api.AgentCapabilityTool{Name: t, Mode: api.AgentCapabilityApprove})
	}
	return p
}

func serveApprovals(t *testing.T, store *fakeApprovalStore, tool CapabilityTool) (*httptest.Server, *CapabilityMetrics) {
	t.Helper()
	metrics, err := NewCapabilityMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewCapabilityGateway(store, []CapabilityTool{tool}, metrics)
	if err != nil {
		t.Fatal(err)
	}
	g.ApprovalPoll = time.Millisecond
	h := &Handler{Capabilities: g}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run := capabilityTestRun
		h.ServeHTTP(w, r.WithContext(withRelayPrincipal(r.Context(), relayPrincipal{agentRun: &run})))
	}))
	t.Cleanup(srv.Close)
	return srv, metrics
}

// adr: 20261027090016 amendment 2 — an approve-mode tool is listed, each
// call waits for the seller's decision, an approved call is counted and
// runs, and a denied, expired or refused one is neither.
func TestCapabilityGatewayHoldsApproveModeCalls(t *testing.T) {
	for _, tc := range []struct {
		name    string
		refuse  bool
		expires time.Duration
		decide  func(int) string
		isErr   bool
		want    string
		outcome string
	}{
		{"approved after a wait", false, time.Hour, func(n int) string {
			if n < 3 {
				return ""
			}
			return api.AgentRunApprovalDecisionApprove
		}, false, "ran for run-1", ApprovalApproved},
		{"denied", false, time.Hour, func(int) string { return api.AgentRunApprovalDecisionDeny }, true, "denied", ApprovalDenied},
		{"expired", false, -time.Minute, func(int) string { return "" }, true, "no one approved", ApprovalExpired},
		{"refused", true, time.Hour, func(int) string { return "" }, true, "cannot ask", ApprovalRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeApprovalStore{fakeCapabilityStore: fakeCapabilityStore{running: true,
				caps: AgentRunCapabilities{Policy: approve("web_fetch"), Plan: api.PlanPro}},
				refuse: tc.refuse, expires: time.Now().Add(tc.expires), decide: tc.decide}
			tool := &fakeTool{name: "web_fetch", enabled: true}
			srv, metrics := serveApprovals(t, store, tool)
			cs := connect(t, srv)
			if got := listed(t, cs); !slices.Equal(got, []string{"web_fetch"}) {
				t.Fatalf("listed %v", got)
			}
			text, isErr := callText(t, cs, "web_fetch")
			if isErr != tc.isErr || !strings.Contains(text, tc.want) {
				t.Fatalf("call: %q error=%v, want %q error=%v", text, isErr, tc.want, tc.isErr)
			}
			ran := 0
			if !tc.isErr {
				ran = 1
			}
			if store.calls != ran || (tool.got != nil) != (ran == 1) {
				t.Errorf("calls counted %d, tool ran %v; want %d", store.calls, tool.got != nil, ran)
			}
			if !tc.refuse && (len(store.requests) != 1 || store.requests[0].Tool != "web_fetch" ||
				store.requests[0].ArgumentsSummary != `{"url":"https://example.com"}` || len(store.requests[0].ArgumentsSHA256) != 64) {
				t.Errorf("requests %+v", store.requests)
			}
			if wantReleased := !tc.refuse; (len(store.released) == 1 && store.released[0] == "ap-1") != wantReleased {
				t.Errorf("released %v, want a release: %v", store.released, wantReleased)
			}
			if got := testutil.ToFloat64(metrics.approvals.WithLabelValues(tc.outcome)); got != 1 {
				t.Errorf("agent_approvals_total{outcome=%q} = %v", tc.outcome, got)
			}
		})
	}
}

// A caller that goes away while its call waits is counted abandoned, and the
// tool never runs.
func TestCapabilityGatewayAbandonedApproval(t *testing.T) {
	store := &fakeApprovalStore{fakeCapabilityStore: fakeCapabilityStore{running: true,
		caps: AgentRunCapabilities{Policy: approve("web_fetch"), Plan: api.PlanPro}},
		expires: time.Now().Add(time.Hour), decide: func(int) string { return "" }}
	tool := &fakeTool{name: "web_fetch", enabled: true}
	metrics, err := NewCapabilityMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewCapabilityGateway(store, []CapabilityTool{tool}, metrics)
	if err != nil {
		t.Fatal(err)
	}
	g.ApprovalPoll = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res, err := g.handler(capabilityRun{run: capabilityTestRun, caps: store.caps}, tool)(ctx, callRequest("web_fetch", `{}`))
	if err == nil || res != nil || tool.got != nil {
		t.Fatalf("abandoned call: %v %v ran=%v", res, err, tool.got != nil)
	}
	if got := testutil.ToFloat64(metrics.approvals.WithLabelValues(ApprovalAbandoned)); got != 1 {
		t.Errorf("abandoned = %v", got)
	}
	if len(store.released) != 1 {
		t.Errorf("an abandoned call was not released: %v", store.released)
	}
}

func TestApprovalRequestSummary(t *testing.T) {
	long := `{"q":"` + strings.Repeat("é", api.AgentRunApprovalSummaryMaxBytes) + `"}`
	req := approvalRequest("web_fetch", []byte(long))
	if len(req.ArgumentsSummary) > api.AgentRunApprovalSummaryMaxBytes || !utf8.ValidString(req.ArgumentsSummary) ||
		!strings.HasPrefix(long, req.ArgumentsSummary) || len(req.ArgumentsSHA256) != 64 {
		t.Errorf("summary of %d bytes, valid=%v", len(req.ArgumentsSummary), utf8.ValidString(req.ArgumentsSummary))
	}
	if got := approvalRequest("web_fetch", []byte{0xff, 0xfe}); got.ArgumentsSummary != "" {
		t.Errorf("invalid UTF-8 kept: %q", got.ArgumentsSummary)
	}
	// A Postgres text column rejects NUL even though it is valid UTF-8.
	if got := approvalRequest("web_fetch", []byte("{\"q\":\"a\x00b\"}")); strings.Contains(got.ArgumentsSummary, "\x00") || got.ArgumentsSummary == "" {
		t.Errorf("NUL kept or summary dropped: %q", got.ArgumentsSummary)
	}
}

func callRequest(name, args string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: name, Arguments: json.RawMessage(args)}}
}
