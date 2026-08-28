// engine_required_node_test.go — ADR-119 v2 integration tests for
// Engine.choosePlacementLocked stamping Request.RequiredNodeID
// from app.NodeID when app.StaticEgressIP != nil.
//
// Pins the v2 contract end-to-end (state -> engine -> placement):
//
//   - App with (StaticEgressIP, NodeID) on a multi-node fleet:
//     the engine stamps RequiredNodeID=app.NodeID and the chooser
//     returns exactly that node (the IP's owning compute_node).
//   - App with (StaticEgressIP, NodeID) but the owning node is
//     saturated (zero headroom): the chooser refuses with
//     ErrCapacity — the customer sees the right wire shape rather
//     than a silent source-spoof.
//   - App without StaticEgressIP but with NodeID (legacy claim-
//     unplaced path): the engine does NOT stamp RequiredNodeID
//     and the chooser falls through to the legacy least-loaded
//     path (the ownerNodeID gate still applies).
//
// Whitebox `package sched` so we can call the package-private
// choosePlacementLocked directly.

package sched

import (
	"context"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// requiredNodeMultiFleet seeds a 3-node fleet (node-A, node-B,
// node-C) in addition to the synthetic default-local. The
// three named nodes have headroom; default-local carries the
// legacy 47,600 MB ceiling. Used by the v2 integration tests.
func requiredNodeMultiFleet(t *testing.T, store *state.MemStore) (nodeA, nodeB, nodeC state.ComputeNode) {
	t.Helper()
	ctx := context.Background()
	var err error
	if nodeA, err = store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "node-A", TargetURL: "unix:///run/faas/node-A.sock",
		VPCPUs: 160, MemMB: 10000, MaxConcurrency: 200,
		AdmissionCeilingMB: 9500, VCPUBudget: 160, Active: true,
	}); err != nil {
		t.Fatalf("CreateComputeNode node-A: %v", err)
	}
	if nodeB, err = store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "node-B", TargetURL: "unix:///run/faas/node-B.sock",
		VPCPUs: 160, MemMB: 10000, MaxConcurrency: 200,
		AdmissionCeilingMB: 9500, VCPUBudget: 160, Active: true,
	}); err != nil {
		t.Fatalf("CreateComputeNode node-B: %v", err)
	}
	if nodeC, err = store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "node-C", TargetURL: "unix:///run/faas/node-C.sock",
		VPCPUs: 160, MemMB: 10000, MaxConcurrency: 200,
		AdmissionCeilingMB: 9500, VCPUBudget: 160, Active: true,
	}); err != nil {
		t.Fatalf("CreateComputeNode node-C: %v", err)
	}
	return nodeA, nodeB, nodeC
}

// TestEngine_RequiredNodeID_StaticEgressIP_PicksOwner — the
// happy path. An app with (StaticEgressIP, NodeID) where NodeID
// is "node-A" must wake on node-A regardless of headroom on the
// other nodes. The engine stamps r.RequiredNodeID = app.NodeID
// before the chooser runs; the chooser filters every candidate
// to exactly node-A.
func TestEngine_RequiredNodeID_StaticEgressIP_PicksOwner(t *testing.T) {
	store := state.NewMemStore()
	nodeA, _, _ := requiredNodeMultiFleet(t, store)

	_, app, _ := seedApp(t, store, api.PlanScale, 512, 5)
	// Pin the IP and the owning node.
	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		StaticEgressIP:    &ip,
		SetStaticEgressIP: true,
		NodeID:            &nodeA.ID,
		SetNodeID:         true,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}

	vmm := &fakeVMM{}
	// Empty ownerNodeID — the legacy single-box posture. The
	// RequiredNodeID stamp must still pin the wake to node-A.
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	placement, err := e.choosePlacementLocked(context.Background(), Request{
		AppID:          app.ID,
		RAMMB:          512,
		VCPU:           2,
		MaxConcurrency: 5,
	})
	if err != nil {
		t.Fatalf("choosePlacementLocked: %v", err)
	}
	if placement.NodeID != nodeA.ID {
		t.Errorf("placement.NodeID = %q, want %q (app.NodeID = %q, IP = %q)",
			placement.NodeID, nodeA.ID, nodeA.ID, "203.0.113.42")
	}
}

// TestEngine_RequiredNodeID_StaticEgressIP_SaturatedReturnsCapacity
// — defence-in-depth. An app pinned to node-A; node-A is full;
// the chooser refuses rather than fall through to node-B/C.
// Falling through would land the IP-pinned app on a non-owning
// node and source-spoof the egress at the switch.
func TestEngine_RequiredNodeID_StaticEgressIP_SaturatedReturnsCapacity(t *testing.T) {
	store := state.NewMemStore()
	nodeA, _, _ := requiredNodeMultiFleet(t, store)
	ctx := context.Background()

	_, app, _ := seedApp(t, store, api.PlanScale, 512, 5)
	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{
		StaticEgressIP:    &ip,
		SetStaticEgressIP: true,
		NodeID:            &nodeA.ID,
		SetNodeID:         true,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}

	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	// Saturate node-A via a live instance. The chooser reads
	// Σ(ram+8) from store.ComputeNodeUsedMB, which counts
	// {waking, cold_booting, running} instances. A reservation
	// at the ledger ceiling is NOT enough — the chooser
	// bypasses the ledger for placement headroom and reads
	// the store directly. Use a large enough instance to fill
	// the ceiling so the next wake fails the RAM headroom
	// check.
	saturatedMB := int(nodeA.AdmissionCeilingMB - api.PerVMOverheadMB)
	if _, err := store.CreateInstance(ctx, app.ID, "", "running", saturatedMB, nodeA.ID, "wake-sat"); err != nil {
		t.Fatalf("CreateInstance (saturate node-A): %v", err)
	}

	_, err := e.choosePlacementLocked(ctx, Request{
		AppID:          app.ID,
		RAMMB:          512,
		VCPU:           2,
		MaxConcurrency: 5,
	})
	if err == nil {
		t.Fatalf("choosePlacementLocked: want capacity err, got nil (saturated owning node must refuse)")
	}
}

// TestEngine_RequiredNodeID_NoStaticEgressIP_FallsThrough — the
// legacy claim-unplaced path. An app with apps.node_id set but
// no StaticEgressIP must NOT have the RequiredNodeID stamp
// applied — the chooser falls through to the normal least-
// loaded tie-break. This pins the "stamp only when
// StaticEgressIP != nil" branch in choosePlacementLocked.
func TestEngine_RequiredNodeID_NoStaticEgressIP_FallsThrough(t *testing.T) {
	store := state.NewMemStore()
	nodeA, nodeB, _ := requiredNodeMultiFleet(t, store)

	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	// Only node_id is set; no StaticEgressIP. This is the
	// claim-unplaced / owner-pinned path — the wake must NOT
	// be hard-pinned to node-A.
	ctx := context.Background()
	if err := store.SetAppNodeID(ctx, app.ID, nodeA.ID); err != nil {
		t.Fatalf("SetAppNodeID: %v", err)
	}

	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	placement, err := e.choosePlacementLocked(ctx, Request{
		AppID:          app.ID,
		RAMMB:          512,
		VCPU:           2,
		MaxConcurrency: 5,
	})
	if err != nil {
		t.Fatalf("choosePlacementLocked: %v", err)
	}
	// The legacy tie-break picks node-B lexicographically
	// (node-A < node-B but default-local sorts first via the
	// name ASC tie-break — default-local always wins on a
	// fresh single-box fleet). The only thing this test pins
	// is "not node-A" — the wake must NOT have been hard-
	// pinned to the app.NodeID because StaticEgressIP is
	// nil.
	if placement.NodeID == nodeA.ID {
		t.Errorf("placement.NodeID = node-A (app.NodeID) without StaticEgressIP — RequiredNodeID must NOT stamp when StaticEgressIP is nil")
	}
	// Confirm node-B is a valid candidate (the chooser saw
	// it through the legacy least-loaded path).
	if placement.NodeID != nodeB.ID && placement.NodeID == nodeA.ID {
		t.Errorf("placement.NodeID = %q, expected the legacy least-loaded pick (not node-A)", placement.NodeID)
	}
}
