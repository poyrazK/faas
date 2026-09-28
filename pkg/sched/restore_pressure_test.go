// spec: §6.2
// adr: 025
package sched

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRestorePressurePlacementPreservesLocalityUntilWatermark(t *testing.T) {
	t.Parallel()
	nodes := []state.ComputeNode{cpuPlacementNode("cached"), cpuPlacementNode("peer")}
	request := Request{
		RAMMB: 128, VCPU: 4, CPUMillicores: 1000,
		PreferredNodeID: "cached", PreferredNodeIDs: []string{"cached"},
		restorePressureAware: true,
	}

	got, err := choosePlacementWithCPU(nodes, nil, nil, map[string]int64{"cached": 0, "peer": 0}, request)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != "cached" {
		t.Fatalf("low-pressure placement = %q, want cached for snapshot locality", got.NodeID)
	}

	request.restorePressureByNode = map[string]int{"cached": restorePressureHighWatermark, "peer": 0}
	got, err = choosePlacementWithCPU(nodes, nil, nil, map[string]int64{"cached": 0, "peer": 0}, request)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != "peer" {
		t.Fatalf("pressured placement = %q, want peer while cached node is at watermark", got.NodeID)
	}
}

func TestRestorePressurePlacementKeepsSmokeLocalityAndSingleFittingNode(t *testing.T) {
	t.Parallel()
	nodes := []state.ComputeNode{cpuPlacementNode("cached"), cpuPlacementNode("peer")}
	request := Request{
		RAMMB: 128, VCPU: 4, CPUMillicores: 1000,
		PreferredNodeID: "cached", PreferredNodeIDs: []string{"cached"},
		PrioritizeSnapshotLocality: true,
		restorePressureAware:       true,
		restorePressureByNode:      map[string]int{"cached": restorePressureHighWatermark, "peer": 0},
	}
	got, err := choosePlacementWithCPU(nodes, nil, nil, map[string]int64{"cached": 0, "peer": 0}, request)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != "cached" {
		t.Fatalf("deployment-smoke placement = %q, want cached snapshot replica", got.NodeID)
	}

	// A fitting peer is required for pressure placement to change the result.
	usedMB := map[string]int64{"cached": 0, "peer": int64(nodes[1].AdmissionCeilingMB)}
	request.PrioritizeSnapshotLocality = false
	got, err = choosePlacementWithCPU(nodes, usedMB, nil, map[string]int64{"cached": 0, "peer": 0}, request)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != "cached" {
		t.Fatalf("single-fitting-node placement = %q, want cached", got.NodeID)
	}
}

func TestChooseRestorePlacementReservesAndReleasesInFlightCounts(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	e := newEngine(t, store, nil, nil, "").WithNodeRegistry(NewNodeRegistry([]state.ComputeNode{
		cpuPlacementNode("cached"), cpuPlacementNode("peer"),
	}))
	request := Request{
		RAMMB: 128, VCPU: 4, CPUMillicores: 1000,
		PreferredNodeID: "cached", PreferredNodeIDs: []string{"cached"},
	}

	first, releaseFirst, err := e.chooseRestorePlacementLocked(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, releaseSecond, err := e.chooseRestorePlacementLocked(context.Background(), request)
	if err != nil {
		releaseFirst()
		t.Fatal(err)
	}
	third, releaseThird, err := e.chooseRestorePlacementLocked(context.Background(), request)
	if err != nil {
		releaseFirst()
		releaseSecond()
		t.Fatal(err)
	}
	if first.NodeID != "cached" || second.NodeID != "cached" {
		t.Fatalf("placements before watermark = %q, %q; want cached, cached", first.NodeID, second.NodeID)
	}
	if third.NodeID != "peer" {
		t.Fatalf("placement at watermark = %q, want peer", third.NodeID)
	}

	releaseFirst()
	releaseFirst() // a deferred cleanup after the explicit RPC release is safe
	releaseSecond()
	releaseThird()
	e.restorePlacementMu.Lock()
	defer e.restorePlacementMu.Unlock()
	if len(e.restorePlacementInFlight) != 0 {
		t.Fatalf("in-flight restore counts after release = %#v, want empty", e.restorePlacementInFlight)
	}
}

func TestWakeBurstSpreadsSnapshotRestoresAfterWatermark(t *testing.T) {
	store := state.NewMemStore()
	cachedNodeID, peerNodeID := seedTwoNodes(t, store)
	ctx := context.Background()
	// Keep the snapshot origin's physical CPU headroom clearly ahead during
	// the first two admissions. This isolates restore pressure from the
	// independent CPU headroom tie-break; once the restore watermark is hit,
	// the peer should win on in-flight restore count.
	cachedNode, err := store.ComputeNodeByID(ctx, cachedNodeID)
	if err != nil {
		t.Fatalf("ComputeNodeByID snapshot origin: %v", err)
	}
	cachedNode.VPCPUs = 320
	if _, err := store.UpsertComputeNode(ctx, cachedNode); err != nil {
		t.Fatalf("UpsertComputeNode snapshot origin: %v", err)
	}
	account, err := store.CreateAccount(ctx, "restore-pressure-burst@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	const wakeCount = 3
	appIDs := make([]string, 0, wakeCount)
	for i := 0; i < wakeCount; i++ {
		app, err := store.CreateApp(ctx, state.App{
			AccountID: account.ID, Slug: fmt.Sprintf("restore-pressure-%d", i),
			RAMMB: 256, MaxConcurrency: wakeCount, IdleTimeoutS: 60,
		})
		if err != nil {
			t.Fatalf("CreateApp %d: %v", i, err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:restore-pressure",
			Status: state.DeployLive,
		})
		if err != nil {
			t.Fatalf("CreateDeployment %d: %v", i, err)
		}
		snap, err := store.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: dep.ID, Tier: state.SnapshotTierInit, FCVersion: "1.10.0",
			MemBytes: 256 << 20, StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "restore-pressure-burst"),
		})
		if err != nil {
			t.Fatalf("CreateSnapshot %d: %v", i, err)
		}
		if err := store.RecordSnapshotOrigin(ctx, snap.ID, cachedNodeID); err != nil {
			t.Fatalf("RecordSnapshotOrigin %d: %v", i, err)
		}
		appIDs = append(appIDs, app.ID)
	}

	vmm := &restorePressureGateVMM{
		fakeVMM: &fakeVMM{}, entered: make(chan string, wakeCount), release: make(chan struct{}),
	}
	defer vmm.unblock()
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithNodeRegistry(NewNodeRegistry(
		[]state.ComputeNode{mustComputeNode(t, store, cachedNodeID), mustComputeNode(t, store, peerNodeID)},
	))
	type wakeResult struct {
		result WakeResult
		err    error
	}
	results := make(chan wakeResult, wakeCount)
	startWake := func(appID string) {
		go func() {
			wakeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			result, err := e.Wake(wakeCtx, appID, "", "", TriggerGateway)
			results <- wakeResult{result: result, err: err}
		}()
	}

	// Hold both initial restore RPCs open. That leaves the scheduler's two
	// reservations visible while the third wake makes its placement decision.
	startWake(appIDs[0])
	waitRestorePressureNode(t, vmm.entered, cachedNodeID, "first restore should use snapshot origin")
	startWake(appIDs[1])
	waitRestorePressureNode(t, vmm.entered, cachedNodeID, "second restore should use snapshot origin before watermark")
	startWake(appIDs[2])
	waitRestorePressureNode(t, vmm.entered, peerNodeID, "third restore should use eligible peer after origin reaches watermark")

	vmm.unblock()
	for i := 0; i < wakeCount; i++ {
		if got := <-results; got.err != nil {
			t.Fatalf("wake %d: %v", i, got.err)
		} else if got.result.Method != vmmdpb.WakeMethod_WAKE_RESTORE {
			t.Errorf("wake %d method = %v, want restore", i, got.result.Method)
		}
	}
	e.restorePlacementMu.Lock()
	defer e.restorePlacementMu.Unlock()
	if len(e.restorePlacementInFlight) != 0 {
		t.Fatalf("in-flight restore counts after burst = %#v, want empty", e.restorePlacementInFlight)
	}
}

func waitRestorePressureNode(t *testing.T, entered <-chan string, want, reason string) {
	t.Helper()
	select {
	case got := <-entered:
		if got != want {
			t.Fatalf("%s: node = %q, want %q", reason, got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for restore RPC: %s", reason)
	}
}

func mustComputeNode(t *testing.T, store state.Store, nodeID string) state.ComputeNode {
	t.Helper()
	node, err := store.ComputeNodeByID(context.Background(), nodeID)
	if err != nil {
		t.Fatalf("ComputeNodeByID %s: %v", nodeID, err)
	}
	return node
}

type restorePressureGateVMM struct {
	*fakeVMM
	entered chan string
	release chan struct{}
	once    sync.Once
}

func (v *restorePressureGateVMM) CreateFromSnapshot(ctx context.Context, nodeID, instance string, app AppSpec, snap SnapshotRef) (*WakeOutcome, error) {
	select {
	case v.entered <- nodeID:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-v.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return v.fakeVMM.CreateFromSnapshot(ctx, nodeID, instance, app, snap)
}

func (v *restorePressureGateVMM) unblock() {
	v.once.Do(func() { close(v.release) })
}
