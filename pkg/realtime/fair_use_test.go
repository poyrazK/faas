package realtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeConnectionSyncer struct {
	mu     sync.Mutex
	remote []state.RealtimeAccountConnections
	sent   [][]state.RealtimeAccountConnections
}

func (f *fakeConnectionSyncer) SyncRealtimeConnections(_ context.Context, counts []state.RealtimeAccountConnections) ([]state.RealtimeAccountConnections, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, counts)
	return f.remote, nil
}

type fairUseNode struct {
	m      *Manager
	server *httptest.Server
}

func newFairUseNode(t *testing.T, cfg Config) *fairUseNode {
	t.Helper()
	cfg.FairUse = true
	m := NewManager(cfg, NopHooks{})
	server := httptest.NewServer(m.Handler())
	t.Cleanup(func() { _ = m.Close(); server.Close() })
	return &fairUseNode{m: m, server: server}
}

func (n *fairUseNode) endpoint(t *testing.T, account string, limit int) string {
	t.Helper()
	id := uuid.NewString()
	if err := n.m.RegisterEndpoint(Endpoint{Preview: true, ID: id, AppID: "app", AccountID: account, AccountMaxConnections: limit,
		Authorize: func(context.Context, *http.Request) (string, error) { return "", nil }}); err != nil {
		t.Fatal(err)
	}
	return id
}

// dial opens a socket, or returns the refusal's status and problem body.
func (n *fairUseNode) dial(t *testing.T, endpoint string) (*websocket.Conn, int, api.Problem) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(n.server.URL, "http") + ManagedPathPrefix + endpoint
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Cleanup(func() { _ = conn.Close() })
		return conn, http.StatusSwitchingProtocols, api.Problem{}
	}
	if resp == nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var problem api.Problem
	_ = json.Unmarshal(body, &problem)
	return nil, resp.StatusCode, problem
}

func (n *fairUseNode) waitOpen(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(n.m.Snapshot()) != want {
		if time.Now().After(deadline) {
			t.Fatalf("open connections = %d, want %d", len(n.m.Snapshot()), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAccountConnectionLimitSpansEndpoints(t *testing.T) {
	node := newFairUseNode(t, Config{MaxConnections: 100})
	account := uuid.NewString()
	first, second := node.endpoint(t, account, 3), node.endpoint(t, account, 3)
	other := node.endpoint(t, uuid.NewString(), 3)
	held := []*websocket.Conn{}
	for _, ep := range []string{first, first, second} {
		conn, status, _ := node.dial(t, ep)
		if status != http.StatusSwitchingProtocols {
			t.Fatalf("connection under the limit refused with %d", status)
		}
		held = append(held, conn)
	}
	_, status, problem := node.dial(t, second)
	if status != http.StatusTooManyRequests || problem.Code != api.CodeRealtimeConnectionLimit || problem.Limit == nil || *problem.Limit != 3 ||
		problem.Observed == nil || *problem.Observed != 3 || problem.DocsURL == "" {
		t.Fatalf("over-limit refusal = %d %+v, want 429 with limit 3 observed 3", status, problem)
	}
	// Another account is unaffected.
	if _, status, _ := node.dial(t, other); status != http.StatusSwitchingProtocols {
		t.Fatalf("other account refused with %d", status)
	}
	// Closing a connection frees the allowance.
	_ = held[0].Close()
	node.waitOpen(t, 3)
	if _, status, _ := node.dial(t, second); status != http.StatusSwitchingProtocols {
		t.Fatalf("connection after a close refused with %d", status)
	}
	if got := node.m.Stats().AccountLimitRefusals; got != 1 {
		t.Fatalf("account refusals = %d, want 1", got)
	}
}

func TestAccountConnectionLimitCountsOtherNodes(t *testing.T) {
	account := uuid.NewString()
	syncer := &fakeConnectionSyncer{remote: []state.RealtimeAccountConnections{{AccountID: account, Connections: 2}}}
	node := newFairUseNode(t, Config{MaxConnections: 100, ConnectionSync: syncer, ConnectionSyncInterval: time.Hour})
	ep := node.endpoint(t, account, 3)
	node.m.syncConnections(t.Context())
	if _, status, _ := node.dial(t, ep); status != http.StatusSwitchingProtocols {
		t.Fatalf("first local connection refused with %d", status)
	}
	node.waitOpen(t, 1)
	if _, status, problem := node.dial(t, ep); status != http.StatusTooManyRequests || *problem.Observed != 3 {
		t.Fatalf("second local connection = %d %+v, want refused at 1 local + 2 remote", status, problem)
	}
	// The next sync reports the local connection.
	node.m.syncConnections(t.Context())
	syncer.mu.Lock()
	last := syncer.sent[len(syncer.sent)-1]
	syncer.mu.Unlock()
	if len(last) != 1 || last[0].AccountID != account || last[0].Connections != 1 {
		t.Fatalf("reported counts = %+v, want the one local connection", last)
	}
	// Remote counts from a stale sync are ignored.
	node.m.connectionSyncedAt.Store(time.Now().Add(-api.RealtimeConnectionSyncStaleAfter - time.Second).UnixNano())
	if _, status, _ := node.dial(t, ep); status != http.StatusSwitchingProtocols {
		t.Fatalf("connection with a stale sync refused with %d, want local-only enforcement", status)
	}
}

func TestAccountNodeShareLimit(t *testing.T) {
	node := newFairUseNode(t, Config{MaxConnections: 4})
	ep := node.endpoint(t, uuid.NewString(), 1_000)
	for range 2 {
		if _, status, _ := node.dial(t, ep); status != http.StatusSwitchingProtocols {
			t.Fatalf("connection under the node share refused with %d", status)
		}
	}
	_, status, problem := node.dial(t, ep)
	if status != http.StatusTooManyRequests || problem.Limit == nil || *problem.Limit != 2 || !strings.Contains(problem.Detail, "single realtime node") {
		t.Fatalf("over-share refusal = %d %+v, want 429 at half of a 4-connection node", status, problem)
	}
	// An endpoint registered without an account limit keeps the old behaviour.
	unlimited := node.endpoint(t, uuid.NewString(), 0)
	for range 2 {
		if _, status, _ := node.dial(t, unlimited); status != http.StatusSwitchingProtocols {
			t.Fatalf("unlimited endpoint refused with %d", status)
		}
	}
	if _, status, problem := node.dial(t, unlimited); status != http.StatusTooManyRequests || problem.Code != "" {
		t.Fatalf("node-full refusal = %d %+v, want the plain node limit", status, problem)
	}
}

// adr: 1323 — without FairUse an account limit is carried and counts are
// synced, but the plan allowance is not enforced, and the node keeps its
// earlier connection default. The per-node account share holds either way
// (amendment 2026-10-24).
func TestFairUseOff(t *testing.T) {
	account := uuid.NewString()
	syncer := &fakeConnectionSyncer{remote: []state.RealtimeAccountConnections{{AccountID: account, Connections: 50}}}
	m := NewManager(Config{MaxConnections: 4, ConnectionSync: syncer, ConnectionSyncInterval: time.Hour}, NopHooks{})
	server := httptest.NewServer(m.Handler())
	t.Cleanup(func() { _ = m.Close(); server.Close() })
	node := &fairUseNode{m: m, server: server}
	ep := node.endpoint(t, account, 1)
	m.syncConnections(t.Context())
	for range 2 { // past the account limit and the remote count, up to the node share
		if _, status, _ := node.dial(t, ep); status != http.StatusSwitchingProtocols {
			t.Fatalf("connection past an unenforced account limit refused with %d", status)
		}
	}
	node.waitOpen(t, 2)
	m.syncConnections(t.Context())
	syncer.mu.Lock()
	last := syncer.sent[len(syncer.sent)-1]
	syncer.mu.Unlock()
	if len(last) != 1 || last[0].Connections != 2 || m.Stats().AccountLimitRefusals+m.Stats().AccountNodeLimitRefusals != 0 {
		t.Fatalf("reported %+v with stats %+v, want 2 synced and no refusals", last, m.Stats())
	}
	// The node share (half of 4) still holds without FairUse: one account
	// cannot take every slot on the node.
	if _, status, problem := node.dial(t, ep); status != http.StatusTooManyRequests || problem.Code != api.CodeRealtimeConnectionLimit {
		t.Fatalf("connection past the node share = %d %+v, want 429 %s", status, problem, api.CodeRealtimeConnectionLimit)
	}
	if m.Stats().AccountNodeLimitRefusals != 1 || m.Stats().AccountLimitRefusals != 0 {
		t.Fatalf("stats %+v, want one node-share refusal", m.Stats())
	}
	for fairUse, want := range map[bool]int{false: api.RealtimeNodeConnectionsWithoutFairUse, true: api.RealtimeNodeConnectionsDefault} {
		if got := (Config{FairUse: fairUse}).withDefaults().MaxConnections; got != want {
			t.Fatalf("node default with FairUse=%v = %d, want %d", fairUse, got, want)
		}
	}
}

func TestEndpointRequestCarriesAccountLimit(t *testing.T) {
	data, err := json.Marshal(endpointRequest{ID: "e", AccountMaxConnections: 200})
	if err != nil {
		t.Fatal(err)
	}
	var decoded endpointRequest
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.endpoint().AccountMaxConnections != 200 {
		t.Fatalf("round trip = %+v, %v", decoded, err)
	}
}
