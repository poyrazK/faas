package pgintegration_test

import (
	"errors"
	"maps"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitOpsRuntimeMigrationCommitsReadinessAndInputsAtomically(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, lease, _, app, deployment, original := runtimeFixture(t, basic, true)
		receipts := basic.(state.RuntimeConfigReceiptStore)
		old, exists, err := receipts.InstanceRuntimeConfigReceipt(t.Context(), original.ID)
		if err != nil || !exists {
			t.Fatalf("source input receipt: %v %v", exists, err)
		}
		destination, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "migration-destination-" + uuid.NewString(), Active: true,
			TargetURL: "tcp://127.0.0.1:50052", AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkInstanceMigrating(t.Context(), original.ID, original.NodeID, "migration-lease"); err != nil {
			t.Fatal(err)
		}
		targets, err := basic.(state.EnvironmentGitOpsRuntimeStore).ObserveEnvironmentGitOpsRuntime(t.Context(), lease)
		if err != nil || len(targets) != 1 || targets[0].StartingResidents != 1 {
			t.Fatalf("in-flight migration was treated as completed runtime: %+v %v", targets, err)
		}
		boundary, _, err := state.RuntimeConfigChangedAtForScope(t.Context(), store, app.ID, deployment.Scope)
		if err != nil {
			t.Fatal(err)
		}
		current := state.RuntimeConfigInputs{Scope: deployment.Scope, Boundary: boundary, Variables: map[string]string{}, SecretVersions: map[string]int64{}}
		rows, err := store.ListAppEnvInScope(t.Context(), app.AccountID, app.ID, deployment.Scope)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			current.Variables[row.Key] = row.Value
		}
		input := state.RuntimeConfigMigration{ExpectedWakeID: original.WakeID, WakeID: uuid.NewString(), Inputs: &current,
			Netns: "destination-netns", HostIP: "10.100.0.18", GuestUID: 20018}
		publisher := basic.(state.RuntimeConfigMigrationStore)
		assertSourceRetained := func() {
			t.Helper()
			instance, err := store.InstanceByID(t.Context(), original.ID)
			proof, exists, proofErr := receipts.InstanceRuntimeConfigReceipt(t.Context(), original.ID)
			if err != nil || instance.NodeID != original.NodeID || instance.WakeID != original.WakeID || instance.State != string(state.StateMigrating) ||
				proofErr != nil || !exists || !maps.Equal(proof.Variables, old.Variables) || !proof.Boundary.Equal(old.Boundary) {
				t.Fatalf("rejected migration changed ownership or discarded evidence: %+v %+v %v %v", instance, proof, err, proofErr)
			}
		}
		wrong := input
		wrong.ExpectedWakeID = uuid.NewString()
		if err := publisher.MigrateInstanceOwnerWithRuntimeConfig(t.Context(), original.ID, original.NodeID, destination.ID, "migration-lease", wrong); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("wrong source wake: %v", err)
		}
		assertSourceRetained()
		wrong = input
		mismatch := current
		mismatch.Scope = "staging"
		wrong.Inputs = &mismatch
		if err := publisher.MigrateInstanceOwnerWithRuntimeConfig(t.Context(), original.ID, original.NodeID, destination.ID, "migration-lease", wrong); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("wrong destination scope: %v", err)
		}
		assertSourceRetained()
		if err := publisher.MigrateInstanceOwnerWithRuntimeConfig(t.Context(), original.ID, original.NodeID, destination.ID, "superseded-lease", input); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("superseded migration lease: %v", err)
		}
		assertSourceRetained()
		if err := publisher.MigrateInstanceOwnerWithRuntimeConfig(t.Context(), original.ID, original.NodeID, destination.ID, "migration-lease", input); err != nil {
			t.Fatal(err)
		}
		moved, err := store.InstanceByID(t.Context(), original.ID)
		proof, exists, proofErr := receipts.InstanceRuntimeConfigReceipt(t.Context(), original.ID)
		if err != nil || moved.State != string(state.StateRunning) || moved.NodeID != destination.ID || moved.WakeID != input.WakeID || moved.HostIP != input.HostIP || moved.Netns != input.Netns || moved.GuestUID != input.GuestUID ||
			!moved.StartedAt.After(original.StartedAt) || proofErr != nil || !exists || !maps.Equal(proof.Variables, current.Variables) || !proof.Boundary.Equal(current.Boundary) {
			t.Fatalf("destination readiness/evidence did not commit together: %+v %+v %v %v", moved, proof, err, proofErr)
		}
		if fresh, err := receipts.RuntimeConfigInputsFresh(t.Context(), app.ID, proof); err != nil || !fresh {
			t.Fatalf("acknowledged destination inputs were stale: %v %v", fresh, err)
		}
		if err := receipts.RecordInstanceRuntimeConfigReceipt(t.Context(), original.ID, original.WakeID, old); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("late source ACK replaced destination: %v", err)
		}
		if _, err := store.PublishSnapshotIfRuntimeFresh(t.Context(), state.Snapshot{DeploymentID: deployment.ID, FCVersion: "1.13.0", StorageKey: state.SnapMemKey(deployment.ID)}, original.ID, original.StartedAt); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
			t.Fatalf("delayed source frame was published after migration: %v", err)
		}
		current.Variables["MODE"] = "mutated caller map"
		proof, _, err = receipts.InstanceRuntimeConfigReceipt(t.Context(), original.ID)
		if err != nil || proof.Variables["MODE"] == current.Variables["MODE"] {
			t.Fatal("migration retained a caller-owned input map")
		}
	})
}

func TestEnvironmentGitOpsRuntimeMigrationWithoutAcknowledgementRemovesSourceEvidence(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown destination method", true: "legacy commit"}[legacy], func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store, lease, _, _, _, original := runtimeFixture(t, basic, true)
				destination, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "migration-unacknowledged-" + uuid.NewString(), Active: true,
					TargetURL: "tcp://127.0.0.1:50052", AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5})
				if err != nil {
					t.Fatal(err)
				}
				oldInputs, oldExists, oldErr := basic.(state.RuntimeConfigReceiptStore).InstanceRuntimeConfigReceipt(t.Context(), original.ID)
				if oldErr != nil || !oldExists {
					t.Fatalf("source receipt: %v %v", oldExists, oldErr)
				}
				if err := store.MarkInstanceMigrating(t.Context(), original.ID, original.NodeID, "migration-lease"); err != nil {
					t.Fatal(err)
				}
				if legacy {
					err = store.MigrateInstanceOwner(t.Context(), original.ID, original.NodeID, destination.ID, "migration-lease")
				} else {
					err = basic.(state.RuntimeConfigMigrationStore).MigrateInstanceOwnerWithRuntimeConfig(t.Context(), original.ID, original.NodeID, destination.ID, "migration-lease",
						state.RuntimeConfigMigration{ExpectedWakeID: original.WakeID, WakeID: uuid.NewString()})
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, exists, err := basic.(state.RuntimeConfigReceiptStore).InstanceRuntimeConfigReceipt(t.Context(), original.ID); err != nil || exists {
					t.Fatalf("unacknowledged destination inherited source proof: %v %v", exists, err)
				}
				if err := basic.(state.RuntimeConfigReceiptStore).RecordInstanceRuntimeConfigReceipt(t.Context(), original.ID, original.WakeID, oldInputs); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("late source acknowledgement resurrected legacy evidence: %v", err)
				}
				targets, err := basic.(state.EnvironmentGitOpsRuntimeStore).ObserveEnvironmentGitOpsRuntime(t.Context(), lease)
				if err != nil || len(targets) != 1 || targets[0].StaleResidents != 1 || targets[0].StartingResidents != 0 {
					t.Fatalf("unacknowledged resident was called converged: %+v %v", targets, err)
				}
			})
		})
	}
}
