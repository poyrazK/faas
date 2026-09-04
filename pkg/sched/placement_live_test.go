// placement_live_test.go — end-to-end chooser tests under a live
// capacity publisher (Tier A1).
//
// These tests exercise Engine.choosePlacementLocked against a
// multi-node fleet where one node's live report says it is
// saturated even though the instances table is empty. The pre-Tier-A
// chooser would have picked that node (no instances → UsedMB=0
// from the store); the post-Tier-A chooser must pick the node the
// live report says has headroom.

package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedTwoNodes inserts two active compute_nodes with equal
// AdmissionCeilingMB so the only differentiator in the chooser is
// the live UsedMB. Mirrors the synthetic default-local bootstrap +
// a second remote box, which is the multi-box fleet shape the
// audit is about. Both rows carry VCPUBudget=api.VCPUSlots (160)
// so the Tier A2 vCPU fit check accepts the test requests.
func seedTwoNodes(t *testing.T, store state.Store) (defaultLocal, remote string) {
	t.Helper()
	ctx := context.Background()
	a, err := store.UpsertComputeNode(ctx, state.ComputeNode{
		Name: state.DefaultLocalNodeName, TargetURL: "unix:///run/faas/vmmd.sock",
		VPCPUs: 160, MemMB: 56000, MaxConcurrency: 200,
		AdmissionCeilingMB: api.RAMAdmissionCeilingMB, VCPUBudget: api.VCPUSlots, Active: true,
	})
	if err != nil {
		t.Fatalf("seed default-local: %v", err)
	}
	b, err := store.UpsertComputeNode(ctx, state.ComputeNode{
		Name: "remote-1", TargetURL: "tcp://10.0.0.42:50051",
		VPCPUs: 160, MemMB: 56000, MaxConcurrency: 200,
		AdmissionCeilingMB: api.RAMAdmissionCeilingMB, VCPUBudget: api.VCPUSlots, Active: true,
	})
	if err != nil {
		t.Fatalf("seed remote-1: %v", err)
	}
	return a.ID, b.ID
}

// TestWakeBurstPlacementSpreadIgnoresWarmHint pins the burst-specific
// placement contract. The warm node has a valid hint but materially less
// headroom; a burst sibling must choose the other active node instead of
// repeatedly following the app's last-warm location.
func TestWakeBurstPlacementSpreadIgnoresWarmHint(t *testing.T) {
	store := state.NewMemStore()
	localID, remoteID := seedTwoNodes(t, store)
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithWarmAffinity(NewWarmAffinity(time.Minute))
	e.capacityTable.Replace(CapacityReport{NodeID: localID, UsedMB: 46000})
	e.capacityTable.Replace(CapacityReport{NodeID: remoteID, UsedMB: 0})
	e.warmAffinity.RecordWake(app.ID, localID)

	got, err := e.Wake(WithBurstPlacementSpread(context.Background()), app.ID, "", "", "gateway")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if got.NodeID != remoteID {
		t.Fatalf("burst placement NodeID = %q, want remote node %q", got.NodeID, remoteID)
	}
}

// TestChoosePlacement_HonorsLiveCapacity pins the Tier A1 invariant:
// the chooser must read the live publisher report, not just the
// store sum. Here the default-local node has a fresh report
// claiming UsedMB=46000 (within the ceiling but with little
// headroom) and remote-1 has UsedMB=0. The store sum is zero for
// both (no instances). The chooser must pick remote-1 — it has
// the most free RAM headroom per the live data.
func TestChoosePlacement_HonorsLiveCapacity(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	localID, remoteID := seedTwoNodes(t, store)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	e.capacityTable.Replace(CapacityReport{NodeID: localID, UsedMB: 46000})
	e.capacityTable.Replace(CapacityReport{NodeID: remoteID, UsedMB: 0})

	// Seed an app; we don't need a full Wake — choosePlacementLocked
	// is the load-bearing call we want to drive.
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	got, err := e.choosePlacementLocked(context.Background(), Request{
		AppID: app.ID, Plan: api.PlanPro, RAMMB: 512, VCPU: 2, MaxConcurrency: 5,
	})
	if err != nil {
		t.Fatalf("choosePlacementLocked: %v", err)
	}
	if got.NodeID != remoteID {
		t.Errorf("NodeID = %q, want %q (chooser ignored the live report)", got.NodeID, remoteID)
	}
}

// TestChoosePlacement_StaleReportFallsBackToStore is the converse
// of the previous test: when the live table has no fresh entry for
// any node, the chooser falls through to the store sum and a real
// instance on local must surface as UsedMB=264 (256 + 8 overhead).
// The pre-Tier-A path did the same thing — the assertion here is
// that the live-publisher lookup did NOT clobber a real
// store-summed value with a 0 sentinel.
func TestChoosePlacement_StaleReportFallsBackToStore(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	localID, remoteID := seedTwoNodes(t, store)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	// No live report for either node. Seed an instance on local
	// so ComputeNodeUsedMB returns 264.
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	if _, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateRunning), 256, localID, "wake-1"); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	got, err := e.choosePlacementLocked(context.Background(), Request{
		AppID: app.ID, Plan: api.PlanPro, RAMMB: 512, VCPU: 2, MaxConcurrency: 5,
	})
	if err != nil {
		t.Fatalf("choosePlacementLocked: %v", err)
	}
	// local: 264 used, ceiling 47600 → fits 512. remote: 0 used, same ceiling → also fits.
	// Tie-break on headroom: remote has 47600 headroom, local has
	// 47336 headroom. Remote wins on headroom.
	// The load-bearing assertion is that BOTH nodes' used_mb maps
	// flowed from the store, not from a stale live-table miss
	// that would have read 0 for both. We assert the usedMB map
	// is observable via the placement's reported UsedMB for the
	// node that actually carries an instance. Since the chooser
	// picked remote (more headroom), we drive a second placement
	// that forces local to win (e.g. saturate remote) and assert
	// the usedMB map for local equals 264.
	if got.NodeID != remoteID {
		t.Errorf("NodeID = %q, want %q (remote has more headroom)", got.NodeID, remoteID)
	}

	// Saturate remote: report the live table to push it over
	// headroom. Now local is the only fit, and the placement
	// must show local's UsedMB from the store sum (264), proving
	// the fallback path is the source.
	e.capacityTable.Replace(CapacityReport{NodeID: remoteID, UsedMB: api.RAMAdmissionCeilingMB})
	got2, err := e.choosePlacementLocked(context.Background(), Request{
		AppID: app.ID, Plan: api.PlanPro, RAMMB: 512, VCPU: 2, MaxConcurrency: 5,
	})
	if err != nil {
		t.Fatalf("choosePlacementLocked (saturated remote): %v", err)
	}
	if got2.NodeID != localID {
		t.Errorf("NodeID = %q, want %q (remote saturated, local only fit)", got2.NodeID, localID)
	}
	if got2.UsedMB != 264 {
		t.Errorf("UsedMB = %d, want 264 (store-sum fallback preserved instance accounting on local)", got2.UsedMB)
	}
}
