package sched

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func recoveryMigrationNode(t *testing.T, store *state.MemStore, name string, lifecycle state.NodeLifecycle) state.ComputeNode {
	t.Helper()
	node, err := store.CreateComputeNode(context.Background(), state.ComputeNode{
		Name:               name,
		TargetURL:          "tcp://" + name + ":50051",
		VPCPUs:             4,
		MemMB:              16_000,
		MaxConcurrency:     20,
		AdmissionCeilingMB: 13_600,
		VCPUBudget:         32,
		Lifecycle:          lifecycle,
		Active:             lifecycle == state.NodeLifecycleActive,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode(%s): %v", name, err)
	}
	return node
}

func disableRecoveryTestDefaultLocal(t *testing.T, store *state.MemStore) {
	t.Helper()
	local, err := store.ComputeNodeByName(context.Background(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName(default-local): %v", err)
	}
	if err := store.SetComputeNodeActive(context.Background(), local.ID, false); err != nil {
		t.Fatalf("SetComputeNodeActive(default-local): %v", err)
	}
}

func TestMigrateRecoveryInstance_CentralScheddChoosesActivePeer(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	source := recoveryMigrationNode(t, store, "source", state.NodeLifecycleDraining)
	destination := recoveryMigrationNode(t, store, "destination", state.NodeLifecycleActive)
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 4)
	if err := store.SetAppNodeID(ctx, app.ID, destination.ID); err != nil {
		t.Fatalf("SetAppNodeID: %v", err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), app.RAMMB, source.ID, "wake-central-recovery")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").
		WithOpsMetrics(wire.NewOpsMetrics("schedd"))
	disableRecoveryTestDefaultLocal(t, store)
	if engine.ownerNodeID != "" {
		t.Fatalf("ownerNodeID = %q, want central schedd posture", engine.ownerNodeID)
	}
	if err := engine.MigrateRecoveryInstance(ctx, instance.ID); err != nil {
		t.Fatalf("MigrateRecoveryInstance: %v", err)
	}

	got, err := store.InstanceByID(ctx, instance.ID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if got.State != string(state.StateRunning) || got.NodeID != destination.ID {
		t.Fatalf("instance = state %q node %q, want running on %q", got.State, got.NodeID, destination.ID)
	}
	if vmm.prepares != 1 || vmm.adopts != 1 || vmm.acks != 1 {
		t.Fatalf("migration calls prepare/adopt/ack = %d/%d/%d, want 1/1/1", vmm.prepares, vmm.adopts, vmm.acks)
	}
}

func TestMigrateRecoveryInstance_CentralScheddNoDestinationFails(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	source := recoveryMigrationNode(t, store, "source", state.NodeLifecycleDraining)
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 4)
	if err := store.SetAppNodeID(ctx, app.ID, source.ID); err != nil {
		t.Fatalf("SetAppNodeID: %v", err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), app.RAMMB, source.ID, "wake-no-destination")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	disableRecoveryTestDefaultLocal(t, store)
	err = engine.MigrateRecoveryInstance(ctx, instance.ID)
	if err == nil || !strings.Contains(err.Error(), "choose destination") {
		t.Fatalf("MigrateRecoveryInstance error = %v, want destination-selection failure", err)
	}
	if vmm.prepares != 0 || vmm.adopts != 0 {
		t.Fatalf("migration RPCs prepare/adopt = %d/%d, want 0/0", vmm.prepares, vmm.adopts)
	}
	got, lookupErr := store.InstanceByID(ctx, instance.ID)
	if lookupErr != nil {
		t.Fatalf("InstanceByID: %v", lookupErr)
	}
	if got.State != string(state.StateRunning) || got.NodeID != source.ID {
		t.Fatalf("instance changed after failed placement: state=%q node=%q", got.State, got.NodeID)
	}
}
