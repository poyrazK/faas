// adr: 585
package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type unavailableWarmPoolCandidates struct{ *state.MemStore }

func (s unavailableWarmPoolCandidates) WarmPoolReconciliationAppIDs(context.Context, string) ([]string, error) {
	return nil, state.ErrNotFound
}

func TestRunReaperWarmPoolCandidateReadFailureRetriesOwnedApps(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 1, false)
	zero := 0
	if _, err := f.store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{WarmPoolSize: &zero, SetWarmPoolSize: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: f.prod.ID, FCVersion: "1.10.0", MemBytes: 256 << 20, StorageKey: state.SnapshotCaptureMemKey(f.prod.ID, state.SnapshotTierInit, "pool-fallback-test")}); err != nil {
		t.Fatal(err)
	}
	vmm := &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, unavailableWarmPoolCandidates{f.store}, vmm, &fakeNotifier{}, "1.10.0")
	NewLoop(nil, engine, testLog()).runReaper(ctx)
	if vmm.pausedCalls != 1 || vmm.destroys != 0 {
		t.Fatalf("candidate read failure lost pinned recovery: paused=%d destroy=%d", vmm.pausedCalls, vmm.destroys)
	}
}

func TestProductionWarmPoolRetiresIncompatibleAndObsoleteRows(t *testing.T) {
	for _, reason := range []string{"ram", "mode", "ephemeral", "old-release"} {
		t.Run(reason, func(t *testing.T) {
			ctx := t.Context()
			f := seedStageSnapshotPolicy(t, 1, false)
			ram, mode := 256, string(state.InstanceModeNormal)
			if reason == "ram" {
				ram = 512
			}
			if reason == "mode" {
				mode = string(state.InstanceModeMirror)
			}
			production, err := f.store.CreateInstanceWithMode(ctx, f.app.ID, f.prod.ID, string(state.StateWarm), ram, state.DefaultLocalNodeName, "", mode)
			if err != nil {
				t.Fatal(err)
			}
			stage := stagePoolInstance(t, f, f.stage)
			if reason == "ephemeral" {
				if err := f.store.UpsertAppSecretWithClassInScope(ctx, f.account.ID, f.app.ID, "production", "TOKEN", "key", "1111111111111111", state.SecretClassEphemeral, []byte("ephemeral-sealed")); err != nil {
					t.Fatal(err)
				}
			}
			if reason == "old-release" {
				if err := f.store.MarkDeploymentSuperseded(ctx, f.prod.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "production", Kind: state.DeploymentKindImage, Status: state.DeployLive}); err != nil {
					t.Fatal(err)
				}
			}
			vmm := &fakeVMM{}
			engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
			if err := engine.ReconcileWarmPool(ctx, f.app.ID); err != nil {
				t.Fatal(err)
			}
			got, err := f.store.InstanceByID(ctx, production.ID)
			if err != nil || state.IsLive(got.State) || vmm.destroys != 1 {
				t.Fatalf("incompatible production pool survived: %+v destroy=%d err=%v", got, vmm.destroys, err)
			}
			got, err = f.store.InstanceByID(ctx, stage.ID)
			if err != nil || got.State != string(state.StateWarm) {
				t.Fatalf("production cleanup retired sibling stage: %+v err=%v", got, err)
			}
		})
	}
}

func TestWarmPromotionRetiresOnlySelectedIncompatiblePool(t *testing.T) {
	for _, reason := range []string{"ram", "mode", "ephemeral"} {
		t.Run(reason, func(t *testing.T) {
			ctx := t.Context()
			f := seedStageSnapshotPolicy(t, 1, false)
			ram, mode := 256, string(state.InstanceModeNormal)
			if reason == "ram" {
				ram = 512
			}
			if reason == "mode" {
				mode = string(state.InstanceModeMirror)
			}
			stage, err := f.store.CreateInstanceWithMode(ctx, f.app.ID, f.stage.ID, string(state.StateWarm), ram, state.DefaultLocalNodeName, "", mode)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.SetInstanceRuntime(ctx, stage.ID, "fc-stage", "10.100.0.2", 20001); err != nil {
				t.Fatal(err)
			}
			production := stagePoolInstance(t, f, f.prod)
			if reason == "ephemeral" {
				if err := f.store.UpsertAppSecretWithClassInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", "key", "1111111111111111", state.SecretClassEphemeral, []byte("ephemeral-sealed")); err != nil {
					t.Fatal(err)
				}
			}
			app, err := state.AppForDeployment(ctx, f.store, f.stage)
			if err != nil {
				t.Fatal(err)
			}
			vmm := &warmResumeFakeVMM{fakeVMM: &fakeVMM{}}
			engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
			if _, accepted, err := engine.promoteWarmInstanceLocked(ctx, app, f.account, api.MustLimitsFor(f.account.Plan), f.stage, string(state.InstanceModeNormal)); err != nil || accepted || vmm.resumeCalls != 0 || vmm.destroys != 1 {
				t.Fatalf("incompatible stage reached resume: accepted=%v resume=%d destroy=%d err=%v", accepted, vmm.resumeCalls, vmm.destroys, err)
			}
			got, err := f.store.InstanceByID(ctx, stage.ID)
			if err != nil || got.State != string(state.StateParked) {
				t.Fatalf("selected stale pool survived: %+v err=%v", got, err)
			}
			got, err = f.store.InstanceByID(ctx, production.ID)
			if err != nil || got.State != string(state.StateWarm) {
				t.Fatalf("stage promotion retired production pool: %+v err=%v", got, err)
			}
		})
	}
}

func TestRunReaperProductionWarmPoolRecoversPinnedTargetWithoutNotification(t *testing.T) {
	for _, desired := range []int{0, 1} {
		t.Run(string(rune('0'+desired)), func(t *testing.T) {
			ctx := t.Context()
			f := seedStageSnapshotPolicy(t, desired, false)
			zero := 0
			if _, err := f.store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{WarmPoolSize: &zero, SetWarmPoolSize: true}); err != nil {
				t.Fatal(err)
			}
			stages := []state.Instance{stagePoolInstance(t, f, f.stage), stagePoolInstance(t, f, f.stage)}
			var production state.Instance
			if desired == 0 {
				production = stagePoolInstance(t, f, f.prod)
			} else if _, err := f.store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: f.prod.ID, FCVersion: "1.10.0", MemBytes: 256 << 20, StorageKey: state.SnapshotCaptureMemKey(f.prod.ID, state.SnapshotTierInit, "pool-reaper-test")}); err != nil {
				t.Fatal(err)
			}
			for _, stage := range stages {
				if _, err := f.store.TouchInstancesLastSeen(ctx, []state.InstanceTouch{{InstanceID: stage.ID, LastRequest: time.Now().Add(-time.Hour)}}); err != nil {
					t.Fatal(err)
				}
			}
			vmm := &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}
			engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
			loop := NewLoop(nil, engine, testLog()).WithClock(func() time.Time { return time.Now().Add(2 * time.Hour) })
			loop.runReaper(ctx)
			if vmm.pausedCalls != desired || vmm.destroys != 1-desired {
				t.Fatalf("periodic pool ignored pinned target: paused=%d destroy=%d desired=%d", vmm.pausedCalls, vmm.destroys, desired)
			}
			if desired == 0 {
				got, err := f.store.InstanceByID(ctx, production.ID)
				if err != nil || state.IsLive(got.State) {
					t.Fatalf("missed disable notification retained production VM: %+v err=%v", got, err)
				}
			}
			for _, stage := range stages {
				got, err := f.store.InstanceByID(ctx, stage.ID)
				if err != nil || got.State != string(state.StateWarm) {
					t.Fatalf("periodic production recovery reclaimed stage: %+v err=%v", got, err)
				}
			}
			if desired > 0 {
				instances, err := f.store.ListInstancesForApp(ctx, f.app.ID)
				if err != nil {
					t.Fatal(err)
				}
				warm := 0
				for _, instance := range instances {
					if instance.DeploymentID == f.prod.ID && instance.State == string(state.StateWarm) {
						warm++
					}
				}
				if warm != desired {
					t.Fatalf("idle reaper undid pinned production pool: warm=%d desired=%d", warm, desired)
				}
			}
		})
	}
}
