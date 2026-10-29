// adr: 20261024225612 — a restore lands only on a node whose CPU can carry it.
package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 25: the CPU-compat gate is enforced by placement narrowing, which keys
// on Request.restoreSnapshot. That was set only on the normal wake path, so
// fork (H25-2) and warm-pool (H25-1) restores could land on an incompatible
// node and fault on resume. These pin that both now narrow by CPU.

// cascadeModel's class is supported by the T2CL template; unknownModel's is not
// (mirrors the signatures in cpu_compat_test.go).
const (
	cpuRestoreCascadeModel = "cascadelake/GenuineIntel-6-85-7/aaaaaaaaaaaa"
	cpuRestoreUnknownModel = "unknown/GenuineIntel-6-167-1/cccccccccccc"
)

// cpuRestorePausedVMM satisfies PausedRestoreVMM so ReconcileWarmPool reaches
// placement; the node is already chosen (and CPU-narrowed) before this runs.
type cpuRestorePausedVMM struct{ *fakeVMM }

func (v *cpuRestorePausedVMM) CreatePausedFromSnapshot(_ context.Context, _, instance string, _ AppSpec, _ SnapshotRef) (*WakeOutcome, error) {
	return &WakeOutcome{Instance: instance, LeaseUID: 20001, HostIP: "10.100.0.2", Netns: "fc-" + instance}, nil
}

func createTemplatedSnapshot(t *testing.T, store *state.MemStore, depID, name, template, model string) {
	t.Helper()
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: depID, FCVersion: "1.10.0", Tier: state.SnapshotTierInit, CreatedAt: time.Now(),
		StorageKey:  state.SnapshotCaptureMemKey(depID, state.SnapshotTierInit, name),
		CPUTemplate: template, CPUModel: model,
	}); err != nil {
		t.Fatal(err)
	}
}

func setComputeNodeCPUModel(t *testing.T, store *state.MemStore, nodeID, model string) {
	t.Helper()
	node, err := store.ComputeNodeByID(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	node.CPUModel = model
	if _, err := store.UpsertComputeNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreForkOnlyOnCPUCompatibleNode(t *testing.T) {
	store := state.NewMemStore()
	a, b := seedTwoNodes(t, store)
	setComputeNodeCPUModel(t, store, a, cpuRestoreUnknownModel) // incompatible with T2CL
	setComputeNodeCPUModel(t, store, b, cpuRestoreCascadeModel) // compatible
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	fork := seedForkTarget(t, store, false)
	createTemplatedSnapshot(t, store, fork.DeploymentID, "fork", "T2CL", cpuRestoreCascadeModel)

	restored, err := e.RestoreFork(context.Background(), fork)
	if err != nil {
		t.Fatalf("RestoreFork: %v", err)
	}
	if restored.NodeID != b {
		t.Fatalf("fork restored on node %s, want the CPU-compatible node %s", restored.NodeID, b)
	}
}

func TestRestoreForkRefusedWhenNoCPUCompatibleNode(t *testing.T) {
	store := state.NewMemStore()
	a, b := seedTwoNodes(t, store)
	setComputeNodeCPUModel(t, store, a, cpuRestoreUnknownModel)
	setComputeNodeCPUModel(t, store, b, cpuRestoreUnknownModel)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	fork := seedForkTarget(t, store, false)
	createTemplatedSnapshot(t, store, fork.DeploymentID, "fork", "T2CL", cpuRestoreCascadeModel)

	// No compatible node: a fork has no cold-boot fallback, so it is refused
	// rather than restored onto an incompatible node (which would fault).
	if _, err := e.RestoreFork(context.Background(), fork); err == nil {
		t.Fatal("fork restored with no CPU-compatible node; want a refusal")
	}
	if insts, _ := store.ListInstancesForApp(context.Background(), fork.AppID); len(insts) != 0 {
		for _, ins := range insts {
			if ins.State == string(state.StateRunning) {
				t.Fatalf("fork left a running instance on an incompatible node: %+v", ins)
			}
		}
	}
}

func TestWarmPoolRestoreOnlyOnCPUCompatibleNode(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	a, b := seedTwoNodes(t, store)
	setComputeNodeCPUModel(t, store, a, cpuRestoreUnknownModel) // incompatible
	setComputeNodeCPUModel(t, store, b, cpuRestoreCascadeModel) // compatible
	e := newEngine(t, store, &cpuRestorePausedVMM{fakeVMM: &fakeVMM{}}, &fakeNotifier{}, "1.10.0")

	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	one := 1
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{WarmPoolSize: &one, SetWarmPoolSize: true}); err != nil {
		t.Fatalf("UpdateApp warm pool size: %v", err)
	}
	createTemplatedSnapshot(t, store, dep.ID, "warm", "T2CL", cpuRestoreCascadeModel)

	if err := e.ReconcileWarmPool(ctx, app.ID); err != nil {
		t.Fatalf("ReconcileWarmPool: %v", err)
	}
	insts, err := store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	warm := 0
	for _, ins := range insts {
		if ins.State == string(state.StateFailed) {
			continue
		}
		warm++
		if ins.NodeID != b {
			t.Fatalf("warm instance on node %s, want the CPU-compatible node %s", ins.NodeID, b)
		}
	}
	if warm == 0 {
		t.Fatal("warm pool filled nothing on a fleet with one compatible node")
	}
}
