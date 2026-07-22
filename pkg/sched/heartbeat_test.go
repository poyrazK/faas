// heartbeat_test.go — table-driven coverage for the per-node
// liveness sweep (issue #97 / ADR-025 axis 3, PR #114; reshaped for
// issue #120).
//
// The heartbeat's contract is narrow:
//
//   1. enumerate ActiveComputeNodes (MemStore auto-seeds
//      'default-local', tests can add more via CreateComputeNode);
//   2. For each active node, dial a fresh VMM via the injected
//      HeartbeatDialer (issue #120: every heartbeat pays the dial
//      cost, no router-cache reuse), call Ping, then Close;
//   3. on success: stamp last_heartbeat_at;
//   4. on failure: mark the row inactive;
//   5. never block on a single dead node — log + move on.
//
// These tests exercise every branch without spinning up a real
// scheduler loop or a real Postgres; MemStore covers the Store
// surface, and a hand-rolled fakeDialer covers the HeartbeatDialer
// surface. We assert dial count == active node count per tick so
// a regression that accidentally re-uses a cached conn trips the
// test.

package sched

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// heartbeatFakeDialer implements HeartbeatDialer for tests. It
// counts every Dial call, threads per-target error injection, and
// returns a stub VMM whose Ping applies the same per-target error
// map. The fake's "nodeID" key is the target_url the heartbeat
// passes in (n.TargetURL), which is unique per row by the
// (target_url) CHECK constraint.
//
// Issue #120: the dialer's Dial count is the canonical signal that
// the heartbeat is paying the per-tick dial cost. A regression
// that routes through the router cache (or any cached path) would
// keep the dial count low even on a multi-node fleet — the
// "fresh_dial_per_tick" subtest below asserts that invariant.
type heartbeatFakeDialer struct {
	mu      sync.Mutex
	dials   []string // targetURLs in Dial call order
	dialErr map[string]error
	pingErr map[string]error // targetURL → err returned by Ping
	closed  int              // number of VMM clients closed by the heartbeat
}

func (h *heartbeatFakeDialer) Dial(_ context.Context, target string, _ *tls.Config) (VMM, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dials = append(h.dials, target)
	if err, ok := h.dialErr[target]; ok {
		return nil, err
	}
	return &heartbeatFakeVMM{target: target, dialer: h}, nil
}

// heartbeatFakeVMM is the stub VMM returned by heartbeatFakeDialer.
// Ping applies the per-target error injection; Close bumps the
// dialer's closed counter so tests can verify the heartbeat
// actually closes each fresh conn (no goroutine leak across ticks).
type heartbeatFakeVMM struct {
	target string
	dialer *heartbeatFakeDialer
}

func (h *heartbeatFakeVMM) Ping(_ context.Context) (*PingOutcome, error) {
	h.dialer.mu.Lock()
	defer h.dialer.mu.Unlock()
	if err, ok := h.dialer.pingErr[h.target]; ok {
		return nil, err
	}
	return &PingOutcome{FcVersion: "1.10.0", ServerTime: time.Now()}, nil
}

func (h *heartbeatFakeVMM) CreateColdBoot(context.Context, string, AppSpec) (*WakeOutcome, error) {
	return &WakeOutcome{}, nil
}
func (h *heartbeatFakeVMM) CreateFromSnapshot(context.Context, string, AppSpec, SnapshotRef) (*WakeOutcome, error) {
	return &WakeOutcome{}, nil
}
func (h *heartbeatFakeVMM) PauseAndSnapshot(context.Context, string, string, string, string) (SnapshotBytes, error) {
	return SnapshotBytes{}, nil
}
func (h *heartbeatFakeVMM) Destroy(context.Context, string) error { return nil }
func (h *heartbeatFakeVMM) Close() error {
	h.dialer.mu.Lock()
	h.dialer.closed++
	h.dialer.mu.Unlock()
	return nil
}

// Compile-time assertion that the dialer satisfies HeartbeatDialer.
var _ HeartbeatDialer = (*heartbeatFakeDialer)(nil)

// TestHeartbeat_HealthyNodeStampsTimestamp pins the happy path:
// one active node, Ping succeeds → HeartbeatComputeNode is called
// once and MarkComputeNodeInactive is not called. This is the
// single-box default-local case post-#112 — schedd keeps the
// synthetic row's last_heartbeat_at fresh so the future staleness
// gate (PR #115+) has a known-good baseline.
//
// Issue #120: also asserts dial-count == 1 (one fresh dial per
// tick per node) and close-count == 1 (no leaked client).
func TestHeartbeat_HealthyNodeStampsTimestamp(t *testing.T) {
	store := state.NewMemStore()
	dialer := &heartbeatFakeDialer{}
	h := NewHeartbeat(store, dialer, nil, nil)

	if err := h.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got := len(dialer.dials); got != 1 {
		t.Errorf("Dial calls = %d, want 1 (MemStore auto-seeds default-local)", got)
	}
	if got := dialer.closed; got != 1 {
		t.Errorf("Close calls = %d, want 1 (heartbeat must Close its fresh VMM)", got)
	}
	nodes, err := store.ActiveComputeNodes(context.Background())
	if err != nil {
		t.Fatalf("ActiveComputeNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 active node, got %d", len(nodes))
	}
	if nodes[0].LastHeartbeatAt.IsZero() {
		t.Error("LastHeartbeatAt still zero after Tick — heartbeat stamp didn't land")
	}
	if !nodes[0].Active {
		t.Error("Active flipped to false on a healthy node — false positive")
	}
}

// TestHeartbeat_DeadNodeFlipsInactive pins the failure path: a node
// whose Ping returns an error must be flipped to active=false so
// placement skips it on the next Wake. The flip is the load-bearing
// behaviour — without it, a dead vmmd would keep receiving wakes
// that timeout.
//
// Issue #120: the heartbeat still dials the dead node (Dial cannot
// tell in advance that the conn is sick — that's the whole point of
// the dial-fresh policy); the failure surfaces at Ping time.
func TestHeartbeat_DeadNodeFlipsInactive(t *testing.T) {
	store := state.NewMemStore()
	live, err := store.CreateComputeNode(context.Background(), state.ComputeNode{
		Name:               "node-b",
		TargetURL:          "tcp://10.0.0.2:50051",
		VPCPUs:             8,
		MemMB:              8192,
		MaxConcurrency:     4,
		AdmissionCeilingMB: 4096,
		Active:             true,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	dead, err := store.ComputeNodeByName(context.Background(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName default-local: %v", err)
	}

	dialer := &heartbeatFakeDialer{
		pingErr: map[string]error{dead.TargetURL: errors.New("vmmd unreachable")},
	}
	h := NewHeartbeat(store, dialer, nil, nil)
	if err := h.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Both nodes were dialed + pinged — the failure on one didn't skip the other.
	if got := len(dialer.dials); got != 2 {
		t.Errorf("Dial calls = %d, want 2 (one per active node)", got)
	}
	if got := dialer.closed; got != 2 {
		t.Errorf("Close calls = %d, want 2 (one per dialed VMM)", got)
	}
	deadAfter, err := store.ComputeNodeByID(context.Background(), dead.ID)
	if err != nil {
		t.Fatalf("ComputeNodeByID dead: %v", err)
	}
	if deadAfter.Active {
		t.Error("dead node still active after Tick — MarkComputeNodeInactive didn't land")
	}
	liveAfter, err := store.ComputeNodeByID(context.Background(), live.ID)
	if err != nil {
		t.Fatalf("ComputeNodeByID live: %v", err)
	}
	if !liveAfter.Active {
		t.Error("live node flipped inactive — false positive on the healthy node")
	}
	if liveAfter.LastHeartbeatAt.IsZero() {
		t.Error("live node's LastHeartbeatAt still zero — heartbeat stamp didn't land")
	}
}

// TestHeartbeat_NoActiveNodesIsNoOp pins the empty-fleet case: a
// Tick with no active compute_nodes (e.g. an admin deactivated the
// synthetic default-local before the loop ever saw it) must not
// error and must not call Dial. This protects the very first Tick
// after schedd starts when the migration seed hasn't landed yet
// (CI flakes on a slow migration apply would otherwise trigger a
// loop of errors).
func TestHeartbeat_NoActiveNodesIsNoOp(t *testing.T) {
	store := state.NewMemStore()
	seeded, err := store.ActiveComputeNodes(context.Background())
	if err != nil {
		t.Fatalf("ActiveComputeNodes: %v", err)
	}
	for _, n := range seeded {
		if err := store.MarkComputeNodeInactive(context.Background(), n.ID); err != nil {
			t.Fatalf("MarkComputeNodeInactive: %v", err)
		}
	}
	dialer := &heartbeatFakeDialer{}
	h := NewHeartbeat(store, dialer, nil, nil)
	if err := h.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := len(dialer.dials); got != 0 {
		t.Errorf("Dial calls = %d, want 0 on empty active set", got)
	}
}

// TestHeartbeat_FreshDialPerTick is the issue #120 invariant: every
// Tick pays the dial cost once per active node. A regression that
// routes through the router cache (or any cached path) would drop
// the dial count even though the underlying Ping still succeeds.
// Two ticks ⇒ two dial waves, even with no state change in between.
func TestHeartbeat_FreshDialPerTick(t *testing.T) {
	store := state.NewMemStore()
	dialer := &heartbeatFakeDialer{}
	h := NewHeartbeat(store, dialer, nil, nil)

	for i := 0; i < 3; i++ {
		if err := h.Tick(context.Background()); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
	}
	if got := len(dialer.dials); got != 3 {
		t.Errorf("Dial calls after 3 ticks = %d, want 3 (one per tick per active node)", got)
	}
	if got := dialer.closed; got != 3 {
		t.Errorf("Close calls after 3 ticks = %d, want 3 (no leaked clients)", got)
	}
}

// TestHeartbeat_TableDriven wraps the canonical scenarios into
// the table the package convention prefers (CLAUDE.md: table-driven
// tests). The dedicated tests above are the diagnostic surface
// when one breaks.
func TestHeartbeat_TableDriven(t *testing.T) {
	t.Run("healthy stamps", func(t *testing.T) {
		store := state.NewMemStore()
		dialer := &heartbeatFakeDialer{}
		h := NewHeartbeat(store, dialer, nil, nil)
		if err := h.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		nodes, _ := store.ActiveComputeNodes(context.Background())
		if len(nodes) != 1 || nodes[0].LastHeartbeatAt.IsZero() || !nodes[0].Active {
			t.Errorf("unexpected state: %+v", nodes)
		}
		if len(dialer.dials) != 1 {
			t.Errorf("Dial calls = %d, want 1", len(dialer.dials))
		}
	})
	t.Run("dead flips inactive", func(t *testing.T) {
		store := state.NewMemStore()
		live, err := store.CreateComputeNode(context.Background(), state.ComputeNode{
			Name:               "node-b",
			TargetURL:          "tcp://10.0.0.2:50051",
			VPCPUs:             8,
			MemMB:              8192,
			MaxConcurrency:     4,
			AdmissionCeilingMB: 4096,
			Active:             true,
		})
		if err != nil {
			t.Fatalf("CreateComputeNode: %v", err)
		}
		dead, err := store.ComputeNodeByName(context.Background(), state.DefaultLocalNodeName)
		if err != nil {
			t.Fatalf("ComputeNodeByName default-local: %v", err)
		}
		dialer := &heartbeatFakeDialer{
			pingErr: map[string]error{dead.TargetURL: errors.New("dead")},
		}
		h := NewHeartbeat(store, dialer, nil, nil)
		if err := h.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		deadAfter, _ := store.ComputeNodeByID(context.Background(), dead.ID)
		liveAfter, _ := store.ComputeNodeByID(context.Background(), live.ID)
		if deadAfter.Active || !liveAfter.Active || liveAfter.LastHeartbeatAt.IsZero() {
			t.Errorf("unexpected state — dead.Active=%v live.Active=%v live.Heartbeat=%v",
				deadAfter.Active, liveAfter.Active, liveAfter.LastHeartbeatAt)
		}
	})
	t.Run("no active nodes is no-op", func(t *testing.T) {
		store := state.NewMemStore()
		seeded, _ := store.ActiveComputeNodes(context.Background())
		for _, n := range seeded {
			_ = store.MarkComputeNodeInactive(context.Background(), n.ID)
		}
		dialer := &heartbeatFakeDialer{}
		h := NewHeartbeat(store, dialer, nil, nil)
		if err := h.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if got := len(dialer.dials); got != 0 {
			t.Errorf("Dial calls = %d, want 0", got)
		}
	})
	t.Run("dial-error flips inactive, others still pinged", func(t *testing.T) {
		// Issue #120 review (PR #122): the heartbeat's dead-node
		// test covers the Ping-error leg but not the Dial-error
		// leg explicitly. A regression where Dial's error is
		// silently swallowed (e.g. wrapped into a nil PingOutcome
		// because the conn never came up) would let a node with a
		// half-broken transport appear healthy. This subtest pins
		// the contract: a node whose Dial returns an error is
		// flipped inactive on the same tick, and the failure does
		// NOT short-circuit the sweep — sibling nodes still get
		// their dial + ping.
		store := state.NewMemStore()
		// Add a second node so we can verify "one dial error
		// doesn't poison the rest". CreateComputeNode is the same
		// surface apid's POST /v1/compute-nodes calls.
		live, err := store.CreateComputeNode(context.Background(), state.ComputeNode{
			Name:               "node-b",
			TargetURL:          "tcp://10.0.0.2:50051",
			VPCPUs:             8,
			MemMB:              8192,
			MaxConcurrency:     4,
			AdmissionCeilingMB: 4096,
			Active:             true,
		})
		if err != nil {
			t.Fatalf("CreateComputeNode: %v", err)
		}
		dead, err := store.ComputeNodeByName(context.Background(), state.DefaultLocalNodeName)
		if err != nil {
			t.Fatalf("ComputeNodeByName default-local: %v", err)
		}
		dialer := &heartbeatFakeDialer{
			// Dial fails for the dead node's target URL. The
			// live node's Dial succeeds and Ping returns the
			// happy-path PingOutcome.
			dialErr: map[string]error{dead.TargetURL: errors.New("dial refused")},
		}
		h := NewHeartbeat(store, dialer, nil, nil)
		if err := h.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		// Both nodes had Dial called. Dial on the dead one
		// returned an error; Dial on the live one succeeded and
		// Ping closed cleanly.
		if got := len(dialer.dials); got != 2 {
			t.Errorf("Dial calls = %d, want 2 (one per active node)", got)
		}
		// Dial-error path: the heartbeat never reached the Close
		// call for the dead node (Dial returned before the VMM
		// stub was constructed). So the closed count is 1, not 2.
		// This is the load-bearing distinction vs TestHeartbeat_DeadNodeFlipsInactive:
		// dial-error leaves one Close uncalled, ping-error calls
		// Close on every successful dial. Pinning the count
		// distinguishes the two paths.
		if got := dialer.closed; got != 1 {
			t.Errorf("Close calls = %d, want 1 (dial-error path skips Close on the dead node)", got)
		}
		// Dead node is now inactive (flipped because Dial errored).
		deadAfter, err := store.ComputeNodeByID(context.Background(), dead.ID)
		if err != nil {
			t.Fatalf("ComputeNodeByID dead: %v", err)
		}
		if deadAfter.Active {
			t.Error("dial-error node still active after Tick — MarkComputeNodeInactive didn't land")
		}
		// Live node is still active + heartbeat was stamped.
		liveAfter, err := store.ComputeNodeByID(context.Background(), live.ID)
		if err != nil {
			t.Fatalf("ComputeNodeByID live: %v", err)
		}
		if !liveAfter.Active {
			t.Error("live node flipped inactive — false positive on the healthy sibling")
		}
		if liveAfter.LastHeartbeatAt.IsZero() {
			t.Error("live node's LastHeartbeatAt still zero — heartbeat stamp didn't land")
		}
	})
}
