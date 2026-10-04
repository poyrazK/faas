// adr: 568
package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStageWarmPromotionChecksCapturedConfigBeforeResume(t *testing.T) {
	for _, edit := range []string{"variable", "secret", "sidecar", "unproved", "production"} {
		t.Run(edit, func(t *testing.T) {
			ctx := t.Context()
			f := seedStageSnapshotPolicy(t, 1, false)
			warm := stagePoolInstance(t, f, f.stage)
			production := stagePoolInstance(t, f, f.prod)
			var err error
			switch edit {
			case "variable":
				err = f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE", "edited")
			case "secret":
				err = f.store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", []byte("edited"))
			case "sidecar":
				_, err = f.store.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{DeploymentID: f.stage.ID, SidecarName: "helper", StorageKey: "apps/new-helper.ext4"})
			case "unproved":
				if err = f.store.DeleteInstance(ctx, warm.ID); err == nil {
					warm, err = f.store.CreateInstanceWithMode(ctx, f.app.ID, f.stage.ID, string(state.StateWarm), 256, warm.NodeID, "", string(state.InstanceModeNormal))
				}
				if err == nil {
					err = f.store.SetInstanceRuntime(ctx, warm.ID, "fc-"+warm.ID, "10.100.0.2", 20001)
				}
			case "production":
				err = f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "production", "MODE", "edited")
			}
			if err != nil {
				t.Fatal(err)
			}
			app, err := state.AppForDeployment(ctx, f.store, f.stage)
			if err != nil {
				t.Fatal(err)
			}
			vmm := &stagePoolResumeVMM{stagePoolPausedVMM: &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}}
			engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
			_, accepted, err := engine.promoteWarmInstanceLocked(ctx, app, f.account, api.MustLimitsFor(f.account.Plan), f.stage, string(state.InstanceModeNormal))
			if err != nil {
				t.Fatal(err)
			}
			if edit == "production" {
				if !accepted || vmm.resumeCalls != 1 || vmm.destroys != 0 {
					t.Fatalf("production edit held stage resume: accepted=%v resume=%d destroyed=%d", accepted, vmm.resumeCalls, vmm.destroys)
				}
			} else {
				if accepted || vmm.resumeCalls != 0 || vmm.destroys != 1 {
					t.Fatalf("stale config reached paused guest: accepted=%v resume=%d destroyed=%d", accepted, vmm.resumeCalls, vmm.destroys)
				}
				row, err := f.store.InstanceByID(ctx, warm.ID)
				if err != nil || row.State != string(state.StateParked) || engine.ledger.ResidentFor(warm.ID) {
					t.Fatalf("stale warm row retained capacity: state=%s err=%v", row.State, err)
				}
			}
			row, err := f.store.InstanceByID(ctx, production.ID)
			if err != nil || row.State != string(state.StateWarm) {
				t.Fatalf("stage retirement changed production: %v", err)
			}
		})
	}
}

func TestStageWarmReconcileRetiresCapturedConfigAfterEdit(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 1, false)
	warm, production := stagePoolInstance(t, f, f.stage), stagePoolInstance(t, f, f.prod)
	if err := f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE", "edited"); err != nil {
		t.Fatal(err)
	}
	vmm := &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.ReconcileWarmPool(WithScope(ctx, "stage"), f.app.ID); err != nil {
		t.Fatal(err)
	}
	row, err := f.store.InstanceByID(ctx, warm.ID)
	if err != nil || row.State != string(state.StateParked) || vmm.destroys != 1 {
		t.Fatalf("stale warm config counted toward target: state=%s destroyed=%d err=%v", row.State, vmm.destroys, err)
	}
	row, err = f.store.InstanceByID(ctx, production.ID)
	if err != nil || row.State != string(state.StateWarm) {
		t.Fatalf("stage edit reclaimed production: %v", err)
	}
}
