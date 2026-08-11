package main

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeScheddDial is the test seam for scheddRouter's dial
// closure. Each (target, id) pair maps to a fresh *fakeSchedd so
// tests can assert per-node dial counts.
type fakeScheddDial struct {
	mu     sync.Mutex
	dials  map[string]int
	byNode map[string]scheddgrpc.ScheddClient
	err    error
}

func newFakeScheddDial() *fakeScheddDial {
	return &fakeScheddDial{dials: map[string]int{}, byNode: map[string]scheddgrpc.ScheddClient{}}
}

func (f *fakeScheddDial) Dial(ctx context.Context, target string, _ *tls.Config) (scheddgrpc.ScheddClient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dials[target]++
	if f.err != nil {
		return nil, f.err
	}
	// Always mint a fresh stub so the cache key (node_id) controls
	// identity — production caches per-node_id (not per-target),
	// and the tests want to assert that two distinct nodes with
	// the same dial target still get distinct clients. Production
	// wire.DialContext produces a fresh *grpc.ClientConn per call
	// when the cache evicts, so the "always fresh" shape is also
	// closer to the real wire behaviour.
	c := &stubSchedd{id: f.dials[target], target: target}
	f.byNode[target] = c
	return c, nil
}

// stubSchedd carries a per-instance serial id so tests can
// distinguish two stubs even when their targets differ in name
// only. The bare pointer (c1 == c2) comparison also works in
// production code; the id is the debug-friendly belt-and-braces.
type stubSchedd struct {
	id     int
	target string
}

func (s *stubSchedd) AdmitInstance(context.Context, string, string) (string, string, string, string, int32, bool, int, error) {
	panic("stubSchedd.AdmitInstance")
}
func (s *stubSchedd) EnsureWake(context.Context, string) (string, string, string, string, int32, int, error) {
	panic("stubSchedd.EnsureWake")
}
func (s *stubSchedd) Wake(context.Context, string, string) (string, string, string, string, int, error) {
	panic("stubSchedd.Wake")
}
func (s *stubSchedd) ReportActivity(context.Context, []state.InstanceTouch) (int, error) {
	panic("stubSchedd.ReportActivity")
}
func (s *stubSchedd) ParkInstance(context.Context, string, string) error {
	panic("stubSchedd.ParkInstance")
}
func (s *stubSchedd) StreamAppLogs(context.Context, string, int64, time.Time, string, string, string) (scheddgrpc.LogStream, error) {
	panic("stubSchedd.StreamAppLogs")
}
func (s *stubSchedd) StreamWarmHints(context.Context) (scheddgrpc.WarmHintStream, error) {
	panic("stubSchedd.StreamWarmHints")
}
func (s *stubSchedd) Close() error { return nil }

// stubSchedd is the bare-bones ScheddClient the router's tests
// don't exercise beyond Close. The full surface is on
// fakeScheddClientAdapter in lastseen_test.go (where ReportActivity
// is exercised); the router's own tests don't dispatch any RPCs,
// so this type's methods panic if a test misroutes one — same
// intent as fakeVMM in pkg/sched. The definition lives below the
// fakeScheddDial type (with id/target fields for debug) since the
// dial closure mints one per call.

// stubRouterStore satisfies the ScheddNodeResolver interface the
// router depends on. Production wires *state.PgStore; tests inject
// a fake so we don't need a live Postgres.
type stubRouterStore struct {
	nodes map[string]state.ComputeNode
	ins   map[string]state.Instance
}

func (s *stubRouterStore) ComputeNodeByID(_ context.Context, id string) (state.ComputeNode, error) {
	n, ok := s.nodes[id]
	if !ok {
		return state.ComputeNode{}, state.ErrNotFound
	}
	return n, nil
}

func (s *stubRouterStore) InstanceByID(_ context.Context, id string) (state.Instance, error) {
	i, ok := s.ins[id]
	if !ok {
		return state.Instance{}, state.ErrNotFound
	}
	return i, nil
}

// TestScheddRouter_CachesPerNode — the router dials once per
// nodeID; subsequent ScheddForApp calls for the same node return
// the cached client without a fresh dial.
func TestScheddRouter_CachesPerNode(t *testing.T) {
	urlA := "tcp://10.0.0.1:7100"
	urlB := "tcp://10.0.0.2:7100"
	nodeA := state.ComputeNode{ID: "node-A", Name: "fsn-1", Active: true, ScheddTargetURL: &urlA}
	nodeB := state.ComputeNode{ID: "node-B", Name: "fsn-2", Active: true, ScheddTargetURL: &urlB}

	store := &stubRouterStore{
		nodes: map[string]state.ComputeNode{"node-A": nodeA, "node-B": nodeB},
	}
	dial := newFakeScheddDial()

	r := newScheddRouter(store, nil, dial.Dial, nil)
	defer func() { _ = r.Close() }()

	// First ScheddForApp for app on node-A → 1 dial to urlA.
	cli1, err := r.ScheddForApp(context.Background(), state.App{ID: "app-1", NodeID: "node-A"})
	if err != nil {
		t.Fatalf("ScheddForApp #1: %v", err)
	}
	// Second ScheddForApp for the same node → still 1 dial total.
	cli2, err := r.ScheddForApp(context.Background(), state.App{ID: "app-2", NodeID: "node-A"})
	if err != nil {
		t.Fatalf("ScheddForApp #2: %v", err)
	}
	if cli1 != cli2 {
		t.Errorf("expected cached client on second lookup, got a fresh one")
	}
	// First ScheddForApp for node-B → 1 dial to urlB.
	cli3, err := r.ScheddForApp(context.Background(), state.App{ID: "app-3", NodeID: "node-B"})
	if err != nil {
		t.Fatalf("ScheddForApp #B: %v", err)
	}
	if cli3 == cli1 {
		t.Errorf("node-B should have its own client, got the node-A client")
	}

	if got := dial.dials[urlA]; got != 1 {
		t.Errorf("dials[%s] = %d, want 1", urlA, got)
	}
	if got := dial.dials[urlB]; got != 1 {
		t.Errorf("dials[%s] = %d, want 1", urlB, got)
	}
}

// TestScheddRouter_EvictClosesAndReDials — Evict drops the cached
// client and forces a fresh dial on the next call.
func TestScheddRouter_EvictClosesAndReDials(t *testing.T) {
	urlA := "tcp://10.0.0.1:7100"
	nodeA := state.ComputeNode{ID: "node-A", Name: "fsn-1", Active: true, ScheddTargetURL: &urlA}
	store := &stubRouterStore{nodes: map[string]state.ComputeNode{"node-A": nodeA}}
	dial := newFakeScheddDial()

	r := newScheddRouter(store, nil, dial.Dial, nil)
	defer func() { _ = r.Close() }()

	cli1, err := r.ScheddForApp(context.Background(), state.App{ID: "app-1", NodeID: "node-A"})
	if err != nil {
		t.Fatalf("ScheddForApp: %v", err)
	}
	if got := dial.dials[urlA]; got != 1 {
		t.Fatalf("dials before evict = %d, want 1", got)
	}
	r.Evict("node-A")
	cli2, err := r.ScheddForApp(context.Background(), state.App{ID: "app-1", NodeID: "node-A"})
	if err != nil {
		t.Fatalf("ScheddForApp after evict: %v", err)
	}
	if cli2 == cli1 {
		t.Errorf("expected fresh client after evict, got the same one")
	}
	if got := dial.dials[urlA]; got != 2 {
		t.Errorf("dials after evict = %d, want 2", got)
	}
}

// TestScheddRouter_RejectsEmptyNodeID — a row that predates the
// migration (or a test fixture with no apps.node_id) must error,
// not silently route to the wrong schedd.
func TestScheddRouter_RejectsEmptyNodeID(t *testing.T) {
	dial := newFakeScheddDial()
	r := newScheddRouter(&stubRouterStore{}, nil, dial.Dial, nil)
	defer func() { _ = r.Close() }()

	if _, err := r.ScheddForApp(context.Background(), state.App{ID: "app-x", NodeID: ""}); err == nil {
		t.Fatal("expected error for empty NodeID, got nil")
	}
	if got := len(dial.dials); got != 0 {
		t.Errorf("dials = %d, want 0 (no dial on empty NodeID)", got)
	}
}

// TestScheddRouter_DialError — a transient dial error surfaces;
// the next call retries with a fresh dial.
func TestScheddRouter_DialError(t *testing.T) {
	urlA := "tcp://10.0.0.1:7100"
	nodeA := state.ComputeNode{ID: "node-A", Name: "fsn-1", Active: true, ScheddTargetURL: &urlA}
	store := &stubRouterStore{nodes: map[string]state.ComputeNode{"node-A": nodeA}}
	dial := newFakeScheddDial()
	dial.err = errors.New("connection refused")

	r := newScheddRouter(store, nil, dial.Dial, nil)
	defer func() { _ = r.Close() }()

	if _, err := r.ScheddForApp(context.Background(), state.App{ID: "app-1", NodeID: "node-A"}); err == nil {
		t.Fatal("expected dial error to surface, got nil")
	}
}

// TestScheddRouter_ScheddForInstance_ResolvesByNodeID — the
// per-instance dispatch does one InstanceByID hop then dials the
// owner schedd (Phase 2 / Gate A). NotFound on the instance
// returns nil,nil so the flush sink drops silently.
func TestScheddRouter_ScheddForInstance_ResolvesByNodeID(t *testing.T) {
	urlA := "tcp://10.0.0.1:7100"
	urlB := "tcp://10.0.0.2:7100"
	nodeA := state.ComputeNode{ID: "node-A", Name: "fsn-1", Active: true, ScheddTargetURL: &urlA}
	nodeB := state.ComputeNode{ID: "node-B", Name: "fsn-2", Active: true, ScheddTargetURL: &urlB}
	store := &stubRouterStore{
		nodes: map[string]state.ComputeNode{"node-A": nodeA, "node-B": nodeB},
		ins: map[string]state.Instance{
			"i-a": {ID: "i-a", AppID: "app-1", NodeID: "node-A"},
			"i-b": {ID: "i-b", AppID: "app-2", NodeID: "node-B"},
		},
	}
	dial := newFakeScheddDial()

	r := newScheddRouter(store, nil, dial.Dial, nil)
	defer func() { _ = r.Close() }()

	cliA, err := r.ScheddForInstance(context.Background(), "i-a")
	if err != nil || cliA == nil {
		t.Fatalf("ScheddForInstance i-a: cli=%v err=%v", cliA, err)
	}
	cliB, err := r.ScheddForInstance(context.Background(), "i-b")
	if err != nil || cliB == nil {
		t.Fatalf("ScheddForInstance i-b: cli=%v err=%v", cliB, err)
	}
	if cliA == cliB {
		t.Errorf("expected distinct per-node clients, got the same instance")
	}
	// Unknown instance → nil, nil (drop silently).
	cliGone, err := r.ScheddForInstance(context.Background(), "i-gone")
	if err != nil {
		t.Errorf("unknown instance should drop silently, got err=%v", err)
	}
	if cliGone != nil {
		t.Errorf("unknown instance should return nil cli, got %v", cliGone)
	}
}
