package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type migrationReceiptVMM struct {
	*fakeVMM
	method    vmmdpb.WakeMethod
	omitWake  bool
	wrongWake bool
	prepared  AppSpec
}

func (f *migrationReceiptVMM) AdoptMigratedInstance(ctx context.Context, node, id string, spec AppSpec, mem, vmstate, lease string) (LiveMigrationAdopt, error) {
	f.prepared = spec
	out, err := f.fakeVMM.AdoptMigratedInstance(ctx, node, id, spec, mem, vmstate, lease)
	if err != nil {
		return out, err
	}
	out.Method = f.method
	if !f.omitWake {
		out.WakeID = spec.migrationRuntime.WakeID
	}
	if f.wrongWake {
		out.WakeID = uuid.NewString()
	}
	return out, nil
}

func TestRuntimeConfigMigrationUsesAcknowledgedRestoreOrColdInputs(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		method                                 vmmdpb.WakeMethod
		omitWake, wrongWake, changeDuringAdopt bool
		wantValue                              string
		wantFresh                              bool
	}{
		{name: "restore inherits old process", method: vmmdpb.WakeMethod_WAKE_RESTORE, wantValue: "original"},
		{name: "cold fallback uses prepared payload", method: vmmdpb.WakeMethod_WAKE_COLD_BOOT, wantValue: "prepared", wantFresh: true},
		{name: "cold fallback loses a concurrent update", method: vmmdpb.WakeMethod_WAKE_COLD_BOOT, changeDuringAdopt: true, wantValue: "prepared"},
		{name: "older peer omits wake proof", method: vmmdpb.WakeMethod_WAKE_COLD_BOOT, omitWake: true},
		{name: "another wake cannot acknowledge inputs", method: vmmdpb.WakeMethod_WAKE_COLD_BOOT, wrongWake: true},
		{name: "unknown method cannot acknowledge inputs", method: vmmdpb.WakeMethod_WAKE_UNKNOWN},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			acct, app, dep := seedApp(t, store, api.PlanHobby, 256, 3)
			if err := store.UpsertAppEnvInScope(t.Context(), acct.ID, app.ID, dep.Scope, "MODE", "original"); err != nil {
				t.Fatal(err)
			}
			vmm := &migrationReceiptVMM{fakeVMM: &fakeVMM{}, method: tc.method, omitWake: tc.omitWake, wrongWake: tc.wrongWake}
			engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			woken, err := engine.Wake(t.Context(), app.ID, "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			original, err := store.InstanceByID(t.Context(), woken.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			old, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), original.ID)
			if err != nil || !exists {
				t.Fatalf("source receipt: %v %v", exists, err)
			}
			if err := store.UpsertAppEnvInScope(t.Context(), acct.ID, app.ID, dep.Scope, "MODE", "prepared"); err != nil {
				t.Fatal(err)
			}
			if tc.changeDuringAdopt {
				vmm.adoptHook = func(string) {
					if err := store.UpsertAppEnvInScope(t.Context(), acct.ID, app.ID, dep.Scope, "MODE", "committed during adoption"); err != nil {
						t.Fatal(err)
					}
				}
			}
			destination, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "migration-runtime-" + uuid.NewString(), Active: true, TargetURL: "tcp://127.0.0.1:50052", AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5})
			if err != nil {
				t.Fatal(err)
			}
			harness := NewMigrationHarness(t.Context(), store, vmm, wire.NewOpsMetrics("schedd"), testLog(), destination.ID, engine.BuildAppSpecForMigration, NewNodeLedger(), nil)
			if err := harness.MigrateOne(t.Context(), original.ID, original.NodeID); err != nil {
				t.Fatal(err)
			}
			moved, err := store.InstanceByID(t.Context(), original.ID)
			if err != nil || moved.NodeID != destination.ID || moved.WakeID == original.WakeID || moved.WakeID != vmm.prepared.migrationRuntime.WakeID || moved.HostIP != "10.100.0.2" {
				t.Fatalf("migration did not publish destination identity: %+v %v", moved, err)
			}
			inputs, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), original.ID)
			if err != nil || exists != (tc.wantValue != "") {
				t.Fatalf("migration input evidence: %+v %v %v", inputs, exists, err)
			}
			if exists {
				if inputs.Variables["MODE"] != tc.wantValue {
					t.Fatalf("wrong boot-path inputs: %+v", inputs)
				}
				fresh, err := store.RuntimeConfigInputsFresh(t.Context(), app.ID, inputs)
				if err != nil || fresh != tc.wantFresh {
					t.Fatalf("runtime freshness: %v %v, want %v", fresh, err, tc.wantFresh)
				}
			}
			if err := store.RecordInstanceRuntimeConfigReceipt(t.Context(), original.ID, original.WakeID, old); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("late source ACK accepted: %v", err)
			}
		})
	}
}

func TestRuntimeConfigMigrationInputReadFailureDoesNotPauseSource(t *testing.T) {
	store := runtimeEnvReadFailingStore{state.NewMemStore()}
	_, app, dep := seedApp(t, store.MemStore, api.PlanHobby, 256, 3)
	nodes, err := store.ListComputeNodes(t.Context(), true)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("source node: %v", err)
	}
	instance, err := store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 256, nodes[0].ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	harness := NewMigrationHarness(t.Context(), store, vmm, wire.NewOpsMetrics("schedd"), testLog(), nodes[0].ID, engine.BuildAppSpecForMigration, NewNodeLedger(), nil)
	if err := harness.MigrateOne(t.Context(), instance.ID, instance.NodeID); err == nil {
		t.Fatal("migration silently discarded API inputs")
	}
	fresh, err := store.InstanceByID(t.Context(), instance.ID)
	if err != nil || fresh.State != string(state.StateRunning) || vmm.prepares != 0 || vmm.adopts != 0 {
		t.Fatalf("input failure paused or moved the source: %+v prepares=%d adopts=%d %v", fresh, vmm.prepares, vmm.adopts, err)
	}
}
