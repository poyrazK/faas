// adr: 585
package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type stagePoolPausedVMM struct {
	*fakeVMM
	pausedCalls int
	pausedHook  func()
	specs       []AppSpec
	snapshots   []SnapshotRef
}

func (v *stagePoolPausedVMM) CreatePausedFromSnapshot(_ context.Context, _, instance string, spec AppSpec, snap SnapshotRef) (*WakeOutcome, error) {
	v.pausedCalls++
	v.specs = append(v.specs, spec)
	v.snapshots = append(v.snapshots, snap)
	if v.pausedHook != nil {
		v.pausedHook()
	}
	return &WakeOutcome{Instance: instance, LeaseUID: 20001, HostIP: "10.100.0.2", Netns: "fc-" + instance}, nil
}

func stagePoolInstance(t *testing.T, f stageSnapshotFixture, dep state.Deployment) state.Instance {
	t.Helper()
	node, err := f.store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := f.store.CreateInstanceWithMode(t.Context(), f.app.ID, dep.ID, string(state.StateWaking), 256, node.ID, "", string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	return publishWarmFixture(t, f.store, instance)
}

func publishWarmFixture(t *testing.T, store state.Store, instance state.Instance) state.Instance {
	t.Helper()
	app, err := store.AppByID(t.Context(), instance.AppID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.RuntimeAppValuesForDeployment(t.Context(), app.AccountID, app.ID, instance.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	warm, err := store.PublishOwnedInstanceRuntime(t.Context(), state.RuntimeInstancePublication{
		AccountID: app.AccountID, AppID: app.ID, InstanceID: instance.ID, NodeID: instance.NodeID, WakeID: instance.WakeID,
		ExpectedState: string(state.StateWaking), TargetState: string(state.StateWarm), Fence: fence.SecretFence, ConfigFence: fence,
		Netns: "fc-" + instance.ID, HostIP: "10.100.0.2", GuestUID: 20001,
	})
	if err != nil {
		t.Fatal(err)
	}
	return warm
}

func TestProductionWarmPoolUsesPinnedTargetWithoutStageCleanup(t *testing.T) {
	for _, desired := range []int{0, 1} {
		t.Run(string(rune('0'+desired)), func(t *testing.T) {
			ctx := t.Context()
			f := seedStageSnapshotPolicy(t, desired, false)
			vmm := &fakeVMM{}
			engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
			production := stagePoolInstance(t, f, f.prod)
			stages := []state.Instance{stagePoolInstance(t, f, f.stage), stagePoolInstance(t, f, f.stage)}
			if err := engine.ReconcileWarmPool(ctx, f.app.ID); err != nil {
				t.Fatal(err)
			}
			got, err := f.store.InstanceByID(ctx, production.ID)
			want := state.StateWarm
			if desired == 0 {
				want = state.StateParked
			}
			if err != nil || got.State != string(want) || vmm.destroys != 1-desired {
				t.Fatalf("production ignored pinned desired count: %+v destroy=%d err=%v", got, vmm.destroys, err)
			}
			for _, stage := range stages {
				got, err := f.store.InstanceByID(ctx, stage.ID)
				if err != nil || got.State != string(state.StateWarm) {
					t.Fatalf("production reconciler reclaimed stage pool: %+v %v", got, err)
				}
			}
		})
	}
}

func TestProductionWarmPoolDoesNotCountSiblingStages(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 1, false)
	vmm := &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	stages := []state.Instance{stagePoolInstance(t, f, f.stage), stagePoolInstance(t, f, f.stage)}
	if _, err := f.store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: f.prod.ID, FCVersion: "1.10.0", MemBytes: 256 << 20, StorageKey: state.SnapshotCaptureMemKey(f.prod.ID, state.SnapshotTierInit, "pool-test")}); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWarmPool(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if vmm.pausedCalls != 1 || vmm.destroys != 0 {
		t.Fatalf("stage rows satisfied production target: paused=%d destroys=%d", vmm.pausedCalls, vmm.destroys)
	}
	instances, err := f.store.ListInstancesForApp(ctx, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	productionWarm := 0
	for _, instance := range instances {
		if instance.State == string(state.StateWarm) && instance.DeploymentID == f.prod.ID {
			productionWarm++
		}
	}
	if productionWarm != 1 {
		t.Fatalf("production pool = %d want one paused VM", productionWarm)
	}
	for _, stage := range stages {
		got, err := f.store.InstanceByID(ctx, stage.ID)
		if err != nil || got.State != string(state.StateWarm) {
			t.Fatalf("production fill mutated stage: %+v %v", got, err)
		}
	}
}

func TestWarmPoolDropsChangedOwnedInputsDuringRestore(t *testing.T) {
	for _, change := range []string{"secret", "plaintext", "ephemeral"} {
		t.Run(change, func(t *testing.T) {
			ctx := t.Context()
			f := seedStageSnapshotPolicy(t, 1, false)
			vmm := &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}
			engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
			stage := stagePoolInstance(t, f, f.stage)
			if _, err := f.store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: f.prod.ID, FCVersion: "1.10.0", MemBytes: 256 << 20, StorageKey: state.SnapshotCaptureMemKey(f.prod.ID, state.SnapshotTierInit, "pool-test")}); err != nil {
				t.Fatal(err)
			}
			vmm.pausedHook = func() {
				var err error
				switch change {
				case "secret":
					err = f.store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "production", "TOKEN", []byte("new-sealed"))
				case "plaintext":
					err = f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "production", "MODE", "new-mode")
				case "ephemeral":
					err = f.store.UpsertAppSecretWithClassInScope(ctx, f.account.ID, f.app.ID, "production", "TOKEN", "key", "1111111111111111", state.SecretClassEphemeral, []byte("ephemeral-sealed"))
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := engine.ReconcileWarmPool(ctx, f.app.ID); err != nil {
				t.Fatal(err)
			}
			if vmm.pausedCalls != 1 || vmm.destroys != 1 {
				t.Fatalf("changed payload became paused capacity: paused=%d destroys=%d", vmm.pausedCalls, vmm.destroys)
			}
			instances, err := f.store.ListInstancesForApp(ctx, f.app.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, instance := range instances {
				if instance.DeploymentID == f.prod.ID && state.IsLive(instance.State) {
					t.Fatalf("stale production restore retained RAM: %+v", instance)
				}
			}
			got, err := f.store.InstanceByID(ctx, stage.ID)
			if err != nil || got.State != string(state.StateWarm) {
				t.Fatalf("failed production restore destroyed stage pool: %+v %v", got, err)
			}
		})
	}
}

func TestWarmPromotionRejectsRecreatedStage(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	vmm := &warmResumeFakeVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	warm := stagePoolInstance(t, f, f.stage)
	if err := f.store.SetInstanceRuntime(ctx, warm.ID, "fc-stage", "10.100.0.2", 20001); err != nil {
		t.Fatal(err)
	}
	app, err := state.AppForDeployment(ctx, f.store, f.stage)
	if err != nil {
		t.Fatal(err)
	}
	f.recreateStage(t.Context(), t)
	if _, accepted, err := engine.promoteWarmInstanceLocked(ctx, app, f.account, api.MustLimitsFor(f.account.Plan), f.stage, string(state.InstanceModeNormal)); err == nil || accepted || vmm.resumeCalls != 0 {
		t.Fatalf("old stage warm VM resumed: accepted=%v resume=%d err=%v", accepted, vmm.resumeCalls, err)
	}
}
