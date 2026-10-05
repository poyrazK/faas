// adr: 568 — environment intent and runtime ownership contracts.
package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRuntimeConfigReceiptRejectsInputsReadBeforeChangeDespiteLaterReadiness(t *testing.T) {
	store := state.NewMemStore()
	account, app, deployment := seedApp(t, store, api.PlanPro, 256, 5)
	if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "default", "MODE", "old"); err != nil {
		t.Fatal(err)
	}
	vmm, notif := &fakeVMM{}, &fakeNotifier{}
	engine := newEngine(t, store, vmm, notif, "1.10.0")
	vmm.coldBootHook = func() {
		if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "default", "MODE", "new"); err != nil {
			t.Error(err)
		}
		if _, err := state.InvalidateAppSnapshotsInScope(t.Context(), store, app.ID, "default"); err != nil {
			t.Error(err)
		}
	}
	if _, err := engine.Wake(t.Context(), app.ID, deployment.ID, "", ""); err == nil {
		t.Fatal("boot published inputs changed during readiness")
	}
	instances, err := store.ListInstancesForApp(t.Context(), app.ID)
	if err != nil || len(instances) != 1 || instances[0].State != string(state.StateFailed) || engine.ledger.Concurrency(app.ID) != 0 || vmm.destroys != 1 {
		t.Fatalf("rejected boot retained capacity or runtime: %+v destroys=%d %v", instances, vmm.destroys, err)
	}
	if _, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), instances[0].ID); err != nil || exists {
		t.Fatalf("rejected boot acknowledged changed inputs: %v %v", exists, err)
	}
	vmm.coldBootHook = nil
	result, err := engine.Wake(t.Context(), app.ID, deployment.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ready, err := store.InstanceByID(t.Context(), result.InstanceID)
	inputs, exists, receiptErr := store.InstanceRuntimeConfigReceipt(t.Context(), ready.ID)
	if err != nil || receiptErr != nil || !exists || inputs.Variables["MODE"] != "new" || engine.runtimeConfigStale(t.Context(), ready) || vmm.coldBoots != 2 || notif.count(db.NotifySnapshotWritten) != 0 {
		t.Fatalf("replacement did not acknowledge current inputs: %+v %+v %v %v", ready, inputs, err, receiptErr)
	}
}

func TestRuntimeConfigReceiptRestoreInheritsCapturedInputs(t *testing.T) {
	store := state.NewMemStore()
	account, app, deployment := seedApp(t, store, api.PlanPro, 256, 5)
	if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "default", "MODE", "captured"); err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	engine := newEngine(t, store, vmm, notif, "1.10.0")
	result, err := engine.Wake(t.Context(), app.ID, deployment.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.InstanceByID(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.PublishSnapshotIfRuntimeFresh(t.Context(), state.Snapshot{
		DeploymentID: deployment.ID, FCVersion: "1.10.0", Tier: state.SnapshotTierInit,
		StorageKey: state.SnapshotCaptureMemKey(deployment.ID, state.SnapshotTierInit, uuid.NewString()),
	}, source.ID, source.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	captured, _, _ := store.SnapshotRuntimeConfigReceipt(t.Context(), snapshot.ID)
	if err := store.UpdateInstanceState(t.Context(), source.ID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	engine.ledger.Release(source.ID)
	// A successful restore acknowledges the captured inputs, not readiness.
	result, err = engine.Wake(t.Context(), app.ID, deployment.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	inputs, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), result.InstanceID)
	if err != nil || !exists || inputs.Variables["MODE"] != "captured" || !inputs.Boundary.Equal(captured.Boundary) {
		t.Fatalf("restore relabelled captured inputs: %+v %v %v", inputs, exists, err)
	}
	if err := store.UpdateInstanceState(t.Context(), result.InstanceID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	engine.ledger.Release(result.InstanceID)
	// The publication fence rejects a config edit racing the restore RPC.
	vmm.restoreHook = func() {
		if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "default", "MODE", "later"); err != nil {
			t.Error(err)
		}
	}
	if _, err := engine.Wake(t.Context(), app.ID, deployment.ID, "", ""); err == nil {
		t.Fatal("restore published configuration changed during readiness")
	}
	instances, err := store.ListInstancesForApp(t.Context(), app.ID)
	if err != nil || len(instances) != 3 || engine.ledger.Concurrency(app.ID) != 0 || vmm.destroys != 1 {
		t.Fatalf("rejected restore retained capacity: %+v %v", instances, err)
	}
	for _, instance := range instances {
		if instance.State == string(state.StateFailed) {
			if _, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), instance.ID); err != nil || exists {
				t.Fatalf("rejected restore wrote an input receipt: %v %v", exists, err)
			}
		}
	}
	unchanged, exists, err := store.SnapshotRuntimeConfigReceipt(t.Context(), snapshot.ID)
	if err != nil || !exists || unchanged.Variables["MODE"] != "captured" || !unchanged.Boundary.Equal(captured.Boundary) {
		t.Fatalf("raced restore changed the capture receipt: %+v %v %v", unchanged, exists, err)
	}
	if vmm.snapshots != 0 || notif.count(db.NotifySnapshotWritten) != 0 {
		t.Fatal("rejected restored process published a new snapshot")
	}
}

type runtimeEnvReadFailingStore struct{ *state.MemStore }

func (runtimeEnvReadFailingStore) RuntimeAppValuesForDeployment(context.Context, string, string, string) (state.RuntimeAppValuesSnapshot, error) {
	return state.RuntimeAppValuesSnapshot{}, errors.New("database unavailable")
}

func TestRuntimeConfigReceiptInputReadFailureRollsBackAdmission(t *testing.T) {
	for _, prime := range []bool{false, true} {
		t.Run(map[bool]string{false: "wake", true: "prime"}[prime], func(t *testing.T) {
			store := runtimeEnvReadFailingStore{state.NewMemStore()}
			_, app, deployment := seedApp(t, store, api.PlanPro, 256, 5)
			vmm := &fakeVMM{}
			engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			var err error
			if prime {
				err = engine.Prime(t.Context(), app.ID, deployment.ID)
			} else {
				_, err = engine.Wake(t.Context(), app.ID, deployment.ID, "", "")
			}
			if err == nil || vmm.coldBoots != 0 || vmm.restores != 0 || engine.ledger.Concurrency(app.ID) != 0 {
				t.Fatalf("failed input read retained admission or dispatched vmmd: %v boots=%d restores=%d concurrent=%d", err, vmm.coldBoots, vmm.restores, engine.ledger.Concurrency(app.ID))
			}
			instances, err := store.ListInstancesForApp(t.Context(), app.ID)
			// Wake reads inputs before creating a row; prime rolls back its
			// provisional row when the same read fails.
			expectedRows := 0
			if prime {
				expectedRows = 1
			}
			if err != nil || len(instances) != expectedRows {
				t.Fatalf("failed preparation retained a resident row: %+v %v", instances, err)
			}
			for _, instance := range instances {
				if state.State(instance.State).CountsForRAM() {
					t.Fatalf("failed preparation retained a resident row: %+v", instance)
				}
			}
		})
	}
}
