// adr: 137 — recovery arbiter and two-node failure-safe workload handoff.
package sched

import (
	"context"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type recoveryRecreateFunc func(context.Context, string) error

func (f recoveryRecreateFunc) RecreateInstance(ctx context.Context, id string) error {
	return f(ctx, id)
}

func TestRecoveryRunnerCompletesEmptyDrain(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "recovery-runner-drain",
		Lifecycle: state.NodeLifecycleActive,
		Active:    true,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := store.NodeSetLifecycle(ctx, node.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
		t.Fatalf("start drain: %v", err)
	}

	runner := NewRecoveryRunner(store, NewArbiter(nil, nil), nil, testLog())
	if err := runner.Tick(ctx); err != nil {
		t.Fatalf("recovery tick: %v", err)
	}

	got, err := store.ComputeNodeByName(ctx, node.Name)
	if err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if got.Lifecycle != state.NodeLifecycleMaintenance || got.Active || got.DrainCompletedAt == nil {
		t.Fatalf("node after empty drain = %+v, want non-admitting maintenance with completion timestamp", got)
	}
}

func TestRecoveryRunnerCompletesEmptyForceDrain(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "recovery-runner-force-drain",
		Lifecycle: state.NodeLifecycleActive,
		Active:    true,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := store.NodeSetLifecycle(ctx, node.ID, state.NodeLifecycleActive, state.NodeLifecycleForceDraining); err != nil {
		t.Fatalf("start force drain: %v", err)
	}
	if err := NewRecoveryRunner(store, NewArbiter(nil, nil), nil, testLog()).Tick(ctx); err != nil {
		t.Fatalf("recovery tick: %v", err)
	}
	got, err := store.ComputeNodeByName(ctx, node.Name)
	if err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if got.Lifecycle != state.NodeLifecycleMaintenance || got.Active || got.DrainCompletedAt == nil {
		t.Fatalf("node after force drain = %+v, want maintenance hold", got)
	}
}

func TestRecoveryRunnerCompletesEmptyRecovery(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "recovery-runner-recover",
		Lifecycle: state.NodeLifecycleActive,
		Active:    true,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := store.NodeSetLifecycle(ctx, node.ID, state.NodeLifecycleActive, state.NodeLifecycleRecovering); err != nil {
		t.Fatalf("start recovery: %v", err)
	}

	runner := NewRecoveryRunner(store, NewArbiter(nil, nil), nil, testLog())
	if err := runner.Tick(ctx); err != nil {
		t.Fatalf("recovery tick: %v", err)
	}

	got, err := store.ComputeNodeByName(ctx, node.Name)
	if err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if got.Lifecycle != state.NodeLifecycleActive || got.LastRecoveryOutcome == nil || *got.LastRecoveryOutcome != "succeeded" {
		t.Fatalf("node after empty recovery = %+v, want active/succeeded", got)
	}
}

func TestRecoveryRunnerRecreatesUnavailableBootWithoutCompletingNode(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "recovery-runner-unavailable",
		Lifecycle: state.NodeLifecycleUnavailable,
		Active:    false,
	})
	if err != nil {
		t.Fatalf("create unavailable node: %v", err)
	}
	instance, err := store.CreateInstance(ctx, "app-recovery-runner", "dep-recovery-runner", string(state.StateColdBooting), 128, node.ID, "wake-recovery-runner")
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	recreated := 0
	arbiter := NewArbiter(nil, recoveryRecreateFunc(func(ctx context.Context, id string) error {
		recreated++
		return store.UpdateInstanceStateIf(ctx, id, string(state.StateColdBooting), string(state.StateParked))
	}))

	runner := NewRecoveryRunner(store, arbiter, nil, testLog())
	if err := runner.Tick(ctx); err != nil {
		t.Fatalf("recovery tick: %v", err)
	}
	if recreated != 1 {
		t.Fatalf("recreate calls = %d, want 1", recreated)
	}
	gotInstance, err := store.InstanceByID(ctx, instance.ID)
	if err != nil {
		t.Fatalf("reload instance: %v", err)
	}
	if gotInstance.State != string(state.StateParked) || gotInstance.ParkedAt.IsZero() {
		t.Fatalf("instance after recreate = %+v, want parked with timestamp", gotInstance)
	}
	gotNode, err := store.ComputeNodeByName(ctx, node.Name)
	if err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if gotNode.Lifecycle != state.NodeLifecycleUnavailable {
		t.Fatalf("unavailable node lifecycle = %q, want unchanged", gotNode.Lifecycle)
	}
}

// TestRecoveryRunnerMigratesTwoNodeWorkload exercises the complete control-
// plane handoff for a node that has gone unavailable. The dispatcher below is
// deliberately backed by the MemStore CAS methods instead of only counting
// calls: a green test therefore proves that the recovery runner drove the
// running → migrating → running transition and moved ownership to the peer.
func TestRecoveryRunnerMigratesTwoNodeWorkload(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	source, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "recovery-workload-source",
		Lifecycle: state.NodeLifecycleUnavailable,
		Active:    false,
	})
	if err != nil {
		t.Fatalf("create source node: %v", err)
	}
	destination, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "recovery-workload-destination",
		Lifecycle: state.NodeLifecycleActive,
		Active:    true,
	})
	if err != nil {
		t.Fatalf("create destination node: %v", err)
	}

	var running []state.Instance
	for i := 0; i < 2; i++ {
		instance, createErr := store.CreateInstance(ctx, "app-recovery-workload", "dep-recovery-workload", string(state.StateRunning), 128, source.ID, fmt.Sprintf("wake-recovery-workload-%d", i))
		if createErr != nil {
			t.Fatalf("create running instance %d: %v", i, createErr)
		}
		running = append(running, instance)
	}
	parked, err := store.CreateInstance(ctx, "app-recovery-workload", "dep-recovery-workload", string(state.StateParked), 128, source.ID, "wake-recovery-workload-parked")
	if err != nil {
		t.Fatalf("create parked instance: %v", err)
	}

	migrated := 0
	arbiter := NewArbiter(MigrationDispatcherFunc(func(ctx context.Context, instanceID string) error {
		_, lookupErr := store.InstanceByID(ctx, instanceID)
		if lookupErr != nil {
			return lookupErr
		}
		leaseToken := "recovery-lease-" + instanceID
		if markErr := store.MarkInstanceMigrating(ctx, instanceID, source.ID, leaseToken); markErr != nil {
			return markErr
		}
		if migrateErr := store.MigrateInstanceOwner(ctx, instanceID, source.ID, destination.ID, leaseToken); migrateErr != nil {
			return migrateErr
		}
		migrated++
		return nil
	}), nil)

	if err := NewRecoveryRunner(store, arbiter, nil, testLog()).Tick(ctx); err != nil {
		t.Fatalf("recovery tick: %v", err)
	}
	if migrated != len(running) {
		t.Fatalf("migrations = %d, want %d", migrated, len(running))
	}
	for _, expected := range running {
		got, lookupErr := store.InstanceByID(ctx, expected.ID)
		if lookupErr != nil {
			t.Fatalf("reload migrated instance %s: %v", expected.ID, lookupErr)
		}
		if got.State != string(state.StateRunning) || got.NodeID != destination.ID {
			t.Fatalf("migrated instance %s = state %q owner %q, want running on %q", got.ID, got.State, got.NodeID, destination.ID)
		}
		if got.MigratedFromNodeID == nil || *got.MigratedFromNodeID != source.ID || got.MigratedAt == nil {
			t.Fatalf("migrated instance %s missing lineage: from=%v at=%v", got.ID, got.MigratedFromNodeID, got.MigratedAt)
		}
	}
	gotParked, err := store.InstanceByID(ctx, parked.ID)
	if err != nil {
		t.Fatalf("reload parked instance: %v", err)
	}
	if gotParked.State != string(state.StateParked) || gotParked.NodeID != source.ID {
		t.Fatalf("parked instance = state %q owner %q, want parked on source", gotParked.State, gotParked.NodeID)
	}
	remaining, err := store.InstanceListByNodeForRecovery(ctx, source.ID)
	if err != nil {
		t.Fatalf("list source recovery rows: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("source recovery rows = %d, want 0 after migration: %+v", len(remaining), remaining)
	}
	gotSource, err := store.ComputeNodeByName(ctx, source.Name)
	if err != nil {
		t.Fatalf("reload source node: %v", err)
	}
	if gotSource.Lifecycle != state.NodeLifecycleUnavailable {
		t.Fatalf("source lifecycle = %q, want unavailable until heartbeat recovery", gotSource.Lifecycle)
	}
}

// TestRecoveryRunnerTwoNodeWorkloadSoakNoStuckRows repeats the same
// state-machine handoff 100 times. This is intentionally a fast control-plane
// soak: it catches duplicate dispatches and rows stranded in migrating state
// without depending on host daemons or CAP_SYS_PTRACE in CI.
func TestRecoveryRunnerTwoNodeWorkloadSoakNoStuckRows(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	source, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "recovery-soak-source",
		Lifecycle: state.NodeLifecycleUnavailable,
		Active:    false,
	})
	if err != nil {
		t.Fatalf("create source node: %v", err)
	}
	destination, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "recovery-soak-destination",
		Lifecycle: state.NodeLifecycleActive,
		Active:    true,
	})
	if err != nil {
		t.Fatalf("create destination node: %v", err)
	}

	migrations := 0
	arbiter := NewArbiter(MigrationDispatcherFunc(func(ctx context.Context, instanceID string) error {
		_, lookupErr := store.InstanceByID(ctx, instanceID)
		if lookupErr != nil {
			return lookupErr
		}
		leaseToken := "recovery-soak-lease-" + instanceID
		if markErr := store.MarkInstanceMigrating(ctx, instanceID, source.ID, leaseToken); markErr != nil {
			return markErr
		}
		if migrateErr := store.MigrateInstanceOwner(ctx, instanceID, source.ID, destination.ID, leaseToken); migrateErr != nil {
			return migrateErr
		}
		migrations++
		return nil
	}), nil)
	runner := NewRecoveryRunner(store, arbiter, nil, testLog())

	for cycle := 0; cycle < 100; cycle++ {
		instance, createErr := store.CreateInstance(ctx, "app-recovery-soak", "dep-recovery-soak", string(state.StateRunning), 128, source.ID, fmt.Sprintf("wake-recovery-soak-%d", cycle))
		if createErr != nil {
			t.Fatalf("cycle %d create instance: %v", cycle, createErr)
		}
		if tickErr := runner.Tick(ctx); tickErr != nil {
			t.Fatalf("cycle %d recovery tick: %v", cycle, tickErr)
		}
		got, lookupErr := store.InstanceByID(ctx, instance.ID)
		if lookupErr != nil {
			t.Fatalf("cycle %d reload instance: %v", cycle, lookupErr)
		}
		if got.State != string(state.StateRunning) || got.NodeID != destination.ID {
			t.Fatalf("cycle %d instance = state %q owner %q, want running on %q", cycle, got.State, got.NodeID, destination.ID)
		}
		if deleteErr := store.DeleteInstance(ctx, instance.ID); deleteErr != nil {
			t.Fatalf("cycle %d delete destination instance: %v", cycle, deleteErr)
		}
		remaining, listErr := store.InstanceListByNodeForRecovery(ctx, source.ID)
		if listErr != nil {
			t.Fatalf("cycle %d list source recovery rows: %v", cycle, listErr)
		}
		if len(remaining) != 0 {
			t.Fatalf("cycle %d source recovery rows = %d, want 0: %+v", cycle, len(remaining), remaining)
		}
	}
	if migrations != 100 {
		t.Fatalf("migrations = %d, want 100", migrations)
	}
}
