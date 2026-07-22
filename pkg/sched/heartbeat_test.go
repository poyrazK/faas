// heartbeat_test.go — table-driven coverage for the per-node
// liveness sweep (issue #97 / ADR-025 axis 3, PR #114).
//
// The heartbeat's contract is narrow:
//
//   1. enumerate ActiveComputeNodes (MemStore auto-seeds
//      'default-local', tests can add more via CreateComputeNode);
//   2. Ping each active node via the injected RoutedVMM;
//   3. on success: stamp last_heartbeat_at;
//   4. on failure: mark the row inactive;
//   5. never block on a single dead node — log + move on.
//
// These tests exercise every branch without spinning up a real
// scheduler loop or a real Postgres; MemStore covers the Store
// surface, and a hand-rolled fakeVMM covers the RoutedVMM surface.

package sched

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// heartbeatFakeVMM records Ping calls and supports a per-node
// error injection (issue #97 / ADR-025 axis 3, PR #114). Mirrors
// the shape of the engine_test fake but is scoped to the heartbeat
// surface — only Ping matters here.
type heartbeatFakeVMM struct {
	mu      sync.Mutex
	pingErr map[string]error // nodeID → err returned by Ping for that node
	pings   []string         // nodeIDs in Ping call order
}

func (h *heartbeatFakeVMM) Ping(_ context.Context, nodeID string) (*PingOutcome, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pings = append(h.pings, nodeID)
	if err, ok := h.pingErr[nodeID]; ok {
		return nil, err
	}
	return &PingOutcome{FcVersion: "1.10.0", ServerTime: time.Now()}, nil
}

// The remaining RoutedVMM methods are no-ops on the heartbeat
// fake. Tests don't exercise the lifecycle RPCs; satisfying the
// interface is enough.
func (h *heartbeatFakeVMM) CreateColdBoot(context.Context, string, string, AppSpec) (*WakeOutcome, error) {
	return &WakeOutcome{}, nil
}
func (h *heartbeatFakeVMM) CreateFromSnapshot(context.Context, string, string, AppSpec, SnapshotRef) (*WakeOutcome, error) {
	return &WakeOutcome{}, nil
}
func (h *heartbeatFakeVMM) PauseAndSnapshot(context.Context, string, string, string, string) (SnapshotBytes, error) {
	return SnapshotBytes{}, nil
}
func (h *heartbeatFakeVMM) Destroy(context.Context, string, string) error { return nil }

// Compile-time assertion.
var _ RoutedVMM = (*heartbeatFakeVMM)(nil)

// TestHeartbeat_HealthyNodeStampsTimestamp pins the happy path:
// one active node, Ping succeeds → HeartbeatComputeNode is called
// once and MarkComputeNodeInactive is not called. This is the
// single-box default-local case post-#112 — schedd keeps the
// synthetic row's last_heartbeat_at fresh so the future staleness
// gate (PR #115+) has a known-good baseline.
func TestHeartbeat_HealthyNodeStampsTimestamp(t *testing.T) {
	store := state.NewMemStore()
	vmm := &heartbeatFakeVMM{}
	h := NewHeartbeat(store, vmm, nil)

	if err := h.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got := len(vmm.pings); got != 1 {
		t.Errorf("Ping calls = %d, want 1 (MemStore auto-seeds default-local)", got)
	}
	// ActiveComputeNodes lists the synthetic default-local. After
	// Tick, last_heartbeat_at must have been bumped. MemStore's
	// getter returns it; the row's id is internal — we read via
	// the Store to assert it's recent.
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
// This is the multi-node scenario where one of N compute_nodes has
// gone dark. The other nodes (if any) keep being heartbeated; one
// bad apple doesn't poison the rest.
func TestHeartbeat_DeadNodeFlipsInactive(t *testing.T) {
	store := state.NewMemStore()
	// Add a second node so we can verify the loop doesn't bail
	// when the first node errors. CreateComputeNode is the same
	// surface apid's POST /v1/compute-nodes calls.
	second, err := store.CreateComputeNode(context.Background(), state.ComputeNode{
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
	// Mark the synthetic default-local row as the dead one.
	// MemStore exposes the ID via ActiveComputeNodes; we read it
	// back and inject a Ping error for it.
	active, _ := store.ActiveComputeNodes(context.Background())
	if len(active) != 2 {
		t.Fatalf("expected 2 active nodes, got %d", len(active))
	}
	deadID := active[0].ID
	liveID := active[1].ID
	if liveID != second.ID {
		// Pin the assumption: ordering is by name (placement /
		// MemStore seed), so default-local is the first row.
		t.Fatalf("expected second.ID to be active[1], got %q / %q", liveID, second.ID)
	}

	vmm := &heartbeatFakeVMM{
		pingErr: map[string]error{deadID: errors.New("vmmd unreachable")},
	}
	h := NewHeartbeat(store, vmm, nil)
	if err := h.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Both nodes were pinged — the failure on one didn't skip the other.
	if got := len(vmm.pings); got != 2 {
		t.Errorf("Ping calls = %d, want 2 (one per active node)", got)
	}
	// The dead node is now inactive.
	dead, err := store.ComputeNodeByID(context.Background(), deadID)
	if err != nil {
		t.Fatalf("ComputeNodeByID dead: %v", err)
	}
	if dead.Active {
		t.Error("dead node still active after Tick — MarkComputeNodeInactive didn't land")
	}
	// The live node is still active and its heartbeat was stamped.
	live, err := store.ComputeNodeByID(context.Background(), liveID)
	if err != nil {
		t.Fatalf("ComputeNodeByID live: %v", err)
	}
	if !live.Active {
		t.Error("live node flipped inactive — false positive on the healthy node")
	}
	if live.LastHeartbeatAt.IsZero() {
		t.Error("live node's LastHeartbeatAt still zero — heartbeat stamp didn't land")
	}
}

// TestHeartbeat_NoActiveNodesIsNoOp pins the empty-fleet case: a
// Tick with no active compute_nodes (e.g. an admin deactivated the
// synthetic default-local before the loop ever saw it) must not
// error and must not call Ping. This protects the very first Tick
// after schedd starts when the migration seed hasn't landed yet
// (CI flakes on a slow migration apply would otherwise trigger a
// loop of errors).
func TestHeartbeat_NoActiveNodesIsNoOp(t *testing.T) {
	store := state.NewMemStore()
	// Auto-seeded default-local exists; mark it inactive to
	// exercise the empty-active-list path.
	seeded, err := store.ActiveComputeNodes(context.Background())
	if err != nil {
		t.Fatalf("ActiveComputeNodes: %v", err)
	}
	for _, n := range seeded {
		if err := store.MarkComputeNodeInactive(context.Background(), n.ID); err != nil {
			t.Fatalf("MarkComputeNodeInactive: %v", err)
		}
	}
	vmm := &heartbeatFakeVMM{}
	h := NewHeartbeat(store, vmm, nil)
	if err := h.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := len(vmm.pings); got != 0 {
		t.Errorf("Ping calls = %d, want 0 on empty active set", got)
	}
}

// TestHeartbeat_TableDriven wraps the three scenarios above into
// the table the package convention prefers (CLAUDE.md: table-driven
// tests). This is the canonical assertion surface — the dedicated
// tests above are the diagnostic surface when one breaks.
func TestHeartbeat_TableDriven(t *testing.T) {
	t.Run("healthy stamps", func(t *testing.T) {
		store := state.NewMemStore()
		vmm := &heartbeatFakeVMM{}
		h := NewHeartbeat(store, vmm, nil)
		if err := h.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		nodes, _ := store.ActiveComputeNodes(context.Background())
		if len(nodes) != 1 || nodes[0].LastHeartbeatAt.IsZero() || !nodes[0].Active {
			t.Errorf("unexpected state: %+v", nodes)
		}
	})
	t.Run("dead flips inactive", func(t *testing.T) {
		store := state.NewMemStore()
		_, err := store.CreateComputeNode(context.Background(), state.ComputeNode{
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
		active, _ := store.ActiveComputeNodes(context.Background())
		vmm := &heartbeatFakeVMM{
			pingErr: map[string]error{active[0].ID: errors.New("dead")},
		}
		h := NewHeartbeat(store, vmm, nil)
		if err := h.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		dead, _ := store.ComputeNodeByID(context.Background(), active[0].ID)
		live, _ := store.ComputeNodeByID(context.Background(), active[1].ID)
		if dead.Active || !live.Active || live.LastHeartbeatAt.IsZero() {
			t.Errorf("unexpected state — dead.Active=%v live.Active=%v live.Heartbeat=%v",
				dead.Active, live.Active, live.LastHeartbeatAt)
		}
	})
	t.Run("no active nodes is no-op", func(t *testing.T) {
		store := state.NewMemStore()
		seeded, _ := store.ActiveComputeNodes(context.Background())
		for _, n := range seeded {
			_ = store.MarkComputeNodeInactive(context.Background(), n.ID)
		}
		vmm := &heartbeatFakeVMM{}
		h := NewHeartbeat(store, vmm, nil)
		if err := h.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if got := len(vmm.pings); got != 0 {
			t.Errorf("Ping calls = %d, want 0", got)
		}
	})
}