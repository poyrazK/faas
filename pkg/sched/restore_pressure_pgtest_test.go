// spec: §6.2
// adr: 025
package sched

import (
	"context"
	"fmt"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWakeBurstSpreadsSnapshotRestoresAcrossPgEngines(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	firstStore := state.NewPgStore(pool)
	secondStore := state.NewPgStore(pool)
	cachedNodeID, peerNodeID := seedTwoNodes(t, firstStore)
	cachedNode, err := firstStore.ComputeNodeByID(ctx, cachedNodeID)
	if err != nil {
		t.Fatalf("ComputeNodeByID snapshot origin: %v", err)
	}
	// Keep the cached origin's ordinary CPU score ahead so this test isolates
	// the shared restore watermark as the reason the third wake spills over.
	cachedNode.VPCPUs = 320
	if _, err := firstStore.UpsertComputeNode(ctx, cachedNode); err != nil {
		t.Fatalf("UpsertComputeNode snapshot origin: %v", err)
	}
	account, err := firstStore.CreateAccount(ctx, "restore-pressure-pg-burst@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	const wakeCount = 3
	appIDs := make([]string, 0, wakeCount)
	for i := 0; i < wakeCount; i++ {
		app, err := firstStore.CreateApp(ctx, state.App{
			AccountID: account.ID, Slug: fmt.Sprintf("restore-pressure-pg-%d", i),
			RAMMB: 256, MaxConcurrency: wakeCount, IdleTimeoutS: 60,
		})
		if err != nil {
			t.Fatalf("CreateApp %d: %v", i, err)
		}
		dep, err := firstStore.CreateDeployment(ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:restore-pressure-pg",
		})
		if err != nil {
			t.Fatalf("CreateDeployment %d: %v", i, err)
		}
		// PgStore creates pending deployment intent; publish it before an
		// unscoped wake asks for the production serving deployment.
		if err := firstStore.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatalf("MarkDeploymentLive %d: %v", i, err)
		}
		snap, err := firstStore.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: dep.ID, Tier: state.SnapshotTierInit, FCVersion: "1.10.0",
			MemBytes:   256 << 20,
			StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "restore-pressure-pg-burst"),
		})
		if err != nil {
			t.Fatalf("CreateSnapshot %d: %v", i, err)
		}
		if err := firstStore.RecordSnapshotOrigin(ctx, snap.ID, cachedNodeID); err != nil {
			t.Fatalf("RecordSnapshotOrigin %d: %v", i, err)
		}
		appIDs = append(appIDs, app.ID)
	}

	nodes := []state.ComputeNode{mustComputeNode(t, firstStore, cachedNodeID), mustComputeNode(t, firstStore, peerNodeID)}
	registry := NewNodeRegistry(nodes)
	vmm := &restorePressureGateVMM{
		fakeVMM: &fakeVMM{}, entered: make(chan string, wakeCount), release: make(chan struct{}),
	}
	defer vmm.unblock()
	firstEngine := newEngine(t, firstStore, vmm, &fakeNotifier{}, "1.10.0").WithNodeRegistry(registry)
	secondEngine := newEngine(t, secondStore, vmm, &fakeNotifier{}, "1.10.0").WithNodeRegistry(registry)

	type wakeResult struct {
		result WakeResult
		err    error
	}
	results := make(chan wakeResult, wakeCount)
	startWake := func(engine *Engine, appID string) {
		go func() {
			wakeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			result, err := engine.Wake(wakeCtx, appID, "", "", TriggerGateway)
			results <- wakeResult{result: result, err: err}
		}()
	}
	waitRestore := func(nodeID, message string) {
		t.Helper()
		select {
		case got := <-vmm.entered:
			if got != nodeID {
				t.Fatalf("%s: node=%s want=%s", message, got, nodeID)
			}
		case got := <-results:
			t.Fatalf("wake ended before restore RPC (%s): %+v %v", message, got.result, got.err)
		case <-time.After(6 * time.Second):
			t.Fatalf("timed out waiting for restore RPC: %s", message)
		}
	}

	// Start the first two restores through independent Engines. Their local
	// counters cannot see each other; the persisted leases must hold both
	// placements visible until the gated vmmd calls return.
	startWake(firstEngine, appIDs[0])
	startWake(secondEngine, appIDs[1])
	waitRestore(cachedNodeID, "first restore should use snapshot origin")
	waitRestore(cachedNodeID, "second restore should use snapshot origin before watermark")
	startWake(firstEngine, appIDs[2])
	waitRestore(peerNodeID, "third restore should use peer after shared watermark")

	vmm.unblock()
	for i := 0; i < wakeCount; i++ {
		got := <-results
		if got.err != nil {
			t.Fatalf("wake %d: %v", i, got.err)
		}
		if got.result.Method != vmmdpb.WakeMethod_WAKE_RESTORE {
			t.Errorf("wake %d method = %v, want restore", i, got.result.Method)
		}
	}

	session, err := firstStore.AcquireSnapshotRestorePressure(ctx)
	if err != nil {
		t.Fatalf("AcquireSnapshotRestorePressure after burst: %v", err)
	}
	counts, err := session.ActiveSnapshotRestoreCounts(ctx)
	session.Close()
	if err != nil {
		t.Fatalf("ActiveSnapshotRestoreCounts after burst: %v", err)
	}
	if len(counts) != 0 {
		t.Fatalf("active restore leases after burst = %#v, want empty", counts)
	}
}
