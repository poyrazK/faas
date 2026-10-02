// adr: 435 — environment intent and runtime ownership contracts.
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
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	engine := newEngine(t, store, vmm, notif, "1.10.0")
	vmm.coldBootHook = func() {
		if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "default", "MODE", "new"); err != nil {
			t.Error(err)
		}
		if _, err := state.InvalidateAppSnapshotsInScope(t.Context(), store, app.ID, "default"); err != nil {
			t.Error(err)
		}
	}
	result, err := engine.Wake(t.Context(), app.ID, deployment.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.InstanceByID(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	boundary, _, err := state.RuntimeConfigChangedAtForScope(t.Context(), store, app.ID, "default")
	if err != nil || !instance.StartedAt.After(boundary) {
		t.Fatalf("fixture did not ready after the config change: %v %v %v", instance.StartedAt, boundary, err)
	}
	inputs, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), instance.ID)
	if err != nil || !exists || inputs.Variables["MODE"] != "old" || !engine.runtimeConfigStale(t.Context(), instance) {
		t.Fatalf("readiness relabelled old inputs: %+v %v %v", inputs, exists, err)
	}
	vmm.coldBootHook = nil
	refreshed, err := engine.RefreshRuntimeConfig(t.Context(), app.ID, uuid.NewString())
	if err != nil || refreshed.Instance == nil || refreshed.Instance.InstanceID == instance.ID {
		t.Fatalf("rolling refresh accepted the stale receipt: %+v %v", refreshed, err)
	}
	ready, err := store.InstanceByID(t.Context(), refreshed.Instance.InstanceID)
	if err != nil || engine.runtimeConfigStale(t.Context(), ready) {
		t.Fatalf("replacement did not acknowledge current inputs: %+v %v", ready, err)
	}
	old, err := store.InstanceByID(t.Context(), instance.ID)
	if err != nil || old.State != string(state.StateStopped) || vmm.coldBoots != 2 || notif.count(db.NotifySnapshotWritten) != 0 {
		t.Fatalf("old process was not retired without capturing: %+v boots=%d snapshots=%d err=%v", old, vmm.coldBoots, notif.count(db.NotifySnapshotWritten), err)
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
	// The selected snapshot was fresh. A new value commits during the
	// restore RPC, after preparation; readiness must inherit the capture.
	vmm.restoreHook = func() {
		if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "default", "MODE", "later"); err != nil {
			t.Error(err)
		}
	}
	result, err = engine.Wake(t.Context(), app.ID, deployment.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	inputs, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), result.InstanceID)
	if err != nil || !exists || inputs.Variables["MODE"] != "captured" || !inputs.Boundary.Equal(captured.Boundary) {
		t.Fatalf("restore relabelled captured inputs: %+v %v %v", inputs, exists, err)
	}
	restored, err := store.InstanceByID(t.Context(), result.InstanceID)
	if err != nil || !engine.runtimeConfigStale(t.Context(), restored) {
		t.Fatalf("restore raced with config and was declared fresh: %+v %v", restored, err)
	}
	if err := engine.Park(t.Context(), result.InstanceID); err != nil {
		t.Fatal(err)
	}
	if vmm.snapshots != 0 || notif.count(db.NotifySnapshotWritten) != 0 {
		t.Fatal("stale restored process published a new snapshot")
	}
}

type runtimeEnvReadFailingStore struct{ *state.MemStore }

func (runtimeEnvReadFailingStore) ListAppEnvInScope(context.Context, string, string, string) ([]state.AppEnv, error) {
	return nil, errors.New("database unavailable")
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
			if err != nil || len(instances) != 1 || state.State(instances[0].State).CountsForRAM() {
				t.Fatalf("failed preparation retained a resident row: %+v %v", instances, err)
			}
		})
	}
}
