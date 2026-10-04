// adr: 531
package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func stagePoolSnapshot(t *testing.T, f stageSnapshotFixture, dep state.Deployment, ram int) {
	t.Helper()
	if _, err := f.store.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: int64(ram) << 20,
		StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "stage-pool-fill")}); err != nil {
		t.Fatal(err)
	}
}

func stagePoolCount(t *testing.T, f stageSnapshotFixture, dep state.Deployment) int {
	t.Helper()
	instances, err := f.store.ListInstancesForApp(t.Context(), f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, ins := range instances {
		if ins.DeploymentID == dep.ID && ins.State == string(state.StateWarm) {
			count++
		}
	}
	return count
}

func TestStageWarmPoolFillsPinnedShapeAndOwnedValues(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB, settings.WarmPoolSize = 512, 2
	stage := stageReaperPolicyDeployment(t, f, "stage", settings)
	stagePoolSnapshot(t, f, stage, 512)
	head, err := f.store.ProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB, settings.WarmPoolSize = 256, 0
	if _, err := f.store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, head.Revision, settings); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if _, err := f.store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{WarmPoolSize: &zero, SetWarmPoolSize: true}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"stage", "production"} {
		if err := f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, scope, "MODE", scope); err != nil {
			t.Fatal(err)
		}
		if err := f.store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, scope, "TOKEN", []byte(scope+"-sealed")); err != nil {
			t.Fatal(err)
		}
	}
	production := stagePoolInstance(t, f, f.prod)
	vmm := &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.ReconcileWarmPool(WithScope(ctx, "stage"), f.app.ID); err != nil {
		t.Fatal(err)
	}
	if vmm.pausedCalls != 2 || vmm.destroys != 0 || stagePoolCount(t, f, stage) != 2 || engine.ledger.Concurrency(f.app.ID) != 0 {
		t.Fatalf("stage fill ignored deployed target: paused=%d destroyed=%d serving=%d", vmm.pausedCalls, vmm.destroys, engine.ledger.Concurrency(f.app.ID))
	}
	for i, spec := range vmm.specs {
		mode := ""
		for _, value := range spec.APIEnv {
			if value.Key == "MODE" {
				mode = value.Value
			}
		}
		if spec.DeploymentID != stage.ID || spec.MemSizeMiB != 512 || mode != "stage" || vmm.snapshots[i].DeploymentID != stage.ID ||
			len(spec.SealedEnv) != 1 || string(spec.SealedEnv[0].Ciphertext) != "stage-sealed" {
			t.Fatalf("stage fill borrowed sibling or desired settings: spec=%+v snapshot=%+v", spec, vmm.snapshots[i])
		}
	}
	row, err := f.store.InstanceByID(ctx, production.ID)
	if err != nil || row.State != string(state.StateWarm) {
		t.Fatalf("stage fill reclaimed production: %+v %v", row, err)
	}
}

func TestEnvironmentWarmPoolsShareAppRestoreBudgetAndRecoverWithoutNotification(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 1, false)
	if _, err := f.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "other"}); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "other", f.app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	other := stageCapacitySettings(t, f, "other", 5, 3)
	for _, dep := range []state.Deployment{f.prod, f.stage, other} {
		stagePoolSnapshot(t, f, dep, 256)
	}
	zero := 0
	if _, err := f.store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{WarmPoolSize: &zero, SetWarmPoolSize: true}); err != nil {
		t.Fatal(err)
	}
	vmm := &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	loop := NewLoop(nil, engine, testLog())
	loop.runReaper(ctx)
	if vmm.pausedCalls != warmPoolRestoreMaxPerTick || stagePoolCount(t, f, f.prod) != 1 {
		t.Fatalf("first pass lost production or exceeded shared budget: calls=%d", vmm.pausedCalls)
	}
	loop.runReaper(ctx)
	if vmm.pausedCalls != 7 || stagePoolCount(t, f, f.prod) != 1 || stagePoolCount(t, f, f.stage) != 3 || stagePoolCount(t, f, other) != 3 || engine.ledger.Concurrency(f.app.ID) != 0 {
		t.Fatalf("periodic stage recovery failed: calls=%d prod=%d stage=%d other=%d serving=%d", vmm.pausedCalls,
			stagePoolCount(t, f, f.prod), stagePoolCount(t, f, f.stage), stagePoolCount(t, f, other), engine.ledger.Concurrency(f.app.ID))
	}
}

func TestStageWarmPoolUsesActiveGraphBeforeNewerDirectDeployment(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	manifest := f.app.Manifest
	manifest.RevisionPinTTLSeconds = 1
	if _, err := f.store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, "stage", 1,
		[]state.ProjectReleaseMember{{AppID: f.app.ID, DeploymentID: f.stage.ID}}); err != nil {
		t.Fatal(err)
	}
	newer := stageCapacitySettings(t, f, "stage", 5, 0)
	stagePoolSnapshot(t, f, f.stage, 256)
	stagePoolSnapshot(t, f, newer, 256)
	vmm := &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.ReconcileWarmPool(WithScope(ctx, "stage"), f.app.ID); err != nil {
		t.Fatal(err)
	}
	if stagePoolCount(t, f, f.stage) != 3 || stagePoolCount(t, f, newer) != 0 {
		t.Fatalf("direct stage deployment displaced graph: graph=%d direct=%d", stagePoolCount(t, f, f.stage), stagePoolCount(t, f, newer))
	}
}

func TestStageWarmPoolDisableRetiresOnlySelectedEnvironment(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 1, false)
	production := stagePoolInstance(t, f, f.prod)
	old := stagePoolInstance(t, f, f.stage)
	stage := stageCapacitySettings(t, f, "stage", 5, 0)
	selected := stagePoolInstance(t, f, stage)
	vmm := &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.ReconcileWarmPool(WithScope(ctx, "stage"), f.app.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{old.ID, selected.ID} {
		row, err := f.store.InstanceByID(ctx, id)
		if err != nil || state.IsLive(row.State) {
			t.Fatalf("disabled stage retained paused capacity: %+v %v", row, err)
		}
	}
	row, err := f.store.InstanceByID(ctx, production.ID)
	if err != nil || row.State != string(state.StateWarm) || vmm.destroys != 2 || vmm.pausedCalls != 0 {
		t.Fatalf("stage disable affected production: %+v %v destroyed=%d", row, err, vmm.destroys)
	}
}

func TestStageWarmPoolRetiresOriginalLifetimeWithoutBorrowingReplacementTarget(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 1, false)
	original := stagePoolInstance(t, f, f.stage)
	f.recreateStage(t)
	stagePoolSnapshot(t, f, f.prod, 256)
	vmm := &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.ReconcileEnvironmentWarmPools(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
	row, err := f.store.InstanceByID(ctx, original.ID)
	if err != nil || state.IsLive(row.State) || vmm.pausedCalls != 1 || stagePoolCount(t, f, f.prod) != 1 {
		t.Fatalf("lost owner survived or blocked sibling recovery: %+v %v paused=%d", row, err, vmm.pausedCalls)
	}
}

func TestRecreatedStageWarmPoolFillsNewLifetime(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 1, false)
	original := stagePoolInstance(t, f, f.stage)
	production := stagePoolInstance(t, f, f.prod)
	f.recreateStage(t)
	if err := f.store.DeleteAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WarmPoolSize = 1
	if _, err := f.store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	stage := stageReaperPolicyDeployment(t, f, "stage", settings)
	stagePoolSnapshot(t, f, stage, 256)
	vmm := &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.ReconcileWarmPool(WithScope(ctx, "stage"), f.app.ID); err != nil {
		t.Fatal(err)
	}
	row, err := f.store.InstanceByID(ctx, original.ID)
	if err != nil || state.IsLive(row.State) || vmm.pausedCalls != 1 || vmm.destroys != 1 || stagePoolCount(t, f, stage) != 1 {
		t.Fatalf("replacement stage adopted old resident capacity: %+v %v paused=%d destroy=%d", row, err, vmm.pausedCalls, vmm.destroys)
	}
	row, err = f.store.InstanceByID(ctx, production.ID)
	if err != nil || row.State != string(state.StateWarm) {
		t.Fatalf("replacement stage reclaimed production: %+v %v", row, err)
	}
}

type stagePoolPublicationStore struct {
	*state.MemStore
	onPublish func(state.RuntimeInstancePublication)
}

func (s *stagePoolPublicationStore) PublishOwnedInstanceRuntime(ctx context.Context, p state.RuntimeInstancePublication) (state.Instance, error) {
	if s.onPublish != nil {
		s.onPublish(p)
	}
	return s.MemStore.PublishOwnedInstanceRuntime(ctx, p)
}

func TestStageWarmPoolPublicationRejectsChangesAfterRestoreRead(t *testing.T) {
	for _, change := range []string{"variable", "secret", "owner", "state"} {
		t.Run(change, func(t *testing.T) {
			ctx := t.Context()
			f := seedStageSnapshotPolicy(t, 1, false)
			stagePoolSnapshot(t, f, f.stage, 256)
			production := stagePoolInstance(t, f, f.prod)
			store := &stagePoolPublicationStore{MemStore: f.store}
			store.onPublish = func(p state.RuntimeInstancePublication) {
				if p.TargetState != string(state.StateWarm) || p.ExpectedState != string(state.StateWaking) || p.Fence.DeploymentID != f.stage.ID {
					t.Fatalf("wrong publication: %+v", p)
				}
				switch change {
				case "owner":
					f.recreateStage(t)
				case "variable":
					if err := f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE", "changed"); err != nil {
						t.Fatal(err)
					}
				case "secret":
					if err := f.store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", []byte("changed")); err != nil {
						t.Fatal(err)
					}
				case "state":
					if err := f.store.UpdateInstanceState(ctx, p.InstanceID, string(state.StateDraining)); err != nil {
						t.Fatal(err)
					}
				}
			}
			vmm := &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}
			engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			if err := engine.ReconcileWarmPool(WithScope(ctx, "stage"), f.app.ID); err != nil {
				t.Fatal(err)
			}
			if vmm.pausedCalls != 1 || vmm.destroys != 1 || stagePoolCount(t, f, f.stage) != 0 {
				t.Fatalf("changed owner/input/state became warm: paused=%d destroyed=%d", vmm.pausedCalls, vmm.destroys)
			}
			rows, err := f.store.ListInstancesForApp(ctx, f.app.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.DeploymentID == f.stage.ID && (row.Netns != "" || row.HostIP != "" || row.GuestUID != 0 ||
					(change == "state" && row.State != string(state.StateDraining))) {
					t.Fatalf("rejected publication exposed runtime or overwrote stolen state: %+v", row)
				}
			}
			row, err := f.store.InstanceByID(ctx, production.ID)
			if err != nil || row.State != string(state.StateWarm) {
				t.Fatalf("publication failure retired production: %+v %v", row, err)
			}
		})
	}
}

type stagePoolUnavailableOwner struct{ *state.MemStore }

func (s stagePoolUnavailableOwner) RuntimeScalingStateForDeployment(ctx context.Context, accountID, appID, deploymentID string) (state.RuntimeScalingState, error) {
	dep, err := s.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return state.RuntimeScalingState{}, err
	}
	if dep.Scope == "stage" {
		return state.RuntimeScalingState{}, errors.New("temporary stage owner read failure")
	}
	return s.MemStore.RuntimeScalingStateForDeployment(ctx, accountID, appID, deploymentID)
}

func TestEnvironmentWarmPoolsFailClosedForUnavailableStageOwner(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 1, false)
	stage := stagePoolInstance(t, f, f.stage)
	stagePoolSnapshot(t, f, f.prod, 256)
	vmm := &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, stagePoolUnavailableOwner{f.store}, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.ReconcileEnvironmentWarmPools(ctx, f.app.ID); err == nil {
		t.Fatal("missing stage owner failure")
	}
	row, err := f.store.InstanceByID(ctx, stage.ID)
	if err != nil || row.State != string(state.StateWarm) || vmm.destroys != 0 || stagePoolCount(t, f, f.prod) != 1 {
		t.Fatalf("failed owner read mutated stage or blocked production: %+v %v", row, err)
	}
}

type stagePoolResumeVMM struct {
	*stagePoolPausedVMM
	resumeCalls int
	resumeHook  func()
}

func (v *stagePoolResumeVMM) ResumeWarmInstance(context.Context, string, string) error {
	v.resumeCalls++
	if v.resumeHook != nil {
		v.resumeHook()
	}
	return nil
}

func TestStageWarmPromotionRejectsChangedOwnerOrInputsDuringResume(t *testing.T) {
	for _, change := range []string{"variable", "secret", "owner"} {
		t.Run(change, func(t *testing.T) {
			ctx := t.Context()
			f := seedStageSnapshotPolicy(t, 1, false)
			stage := stagePoolInstance(t, f, f.stage)
			production := stagePoolInstance(t, f, f.prod)
			if err := f.store.SetInstanceRuntime(ctx, stage.ID, "fc-stage", "10.100.0.2", 20001); err != nil {
				t.Fatal(err)
			}
			app, err := state.AppForDeployment(ctx, f.store, f.stage)
			if err != nil {
				t.Fatal(err)
			}
			vmm := &stagePoolResumeVMM{stagePoolPausedVMM: &stagePoolPausedVMM{fakeVMM: &fakeVMM{}}}
			vmm.resumeHook = func() {
				if change == "owner" {
					f.recreateStage(t)
				} else if change == "variable" {
					if err := f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE", "changed"); err != nil {
						t.Fatal(err)
					}
				} else if err := f.store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", []byte("changed")); err != nil {
					t.Fatal(err)
				}
			}
			engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
			if _, accepted, err := engine.promoteWarmInstanceLocked(ctx, app, f.account, api.MustLimitsFor(f.account.Plan), f.stage, string(state.InstanceModeNormal)); err != nil || accepted || vmm.resumeCalls != 1 || vmm.destroys != 1 {
				t.Fatalf("changed resume published serving capacity: accepted=%v resume=%d destroyed=%d err=%v", accepted, vmm.resumeCalls, vmm.destroys, err)
			}
			row, err := f.store.InstanceByID(ctx, stage.ID)
			if err != nil || row.State != string(state.StateParked) || engine.ledger.ResidentFor(stage.ID) {
				t.Fatalf("failed promotion retained RAM: %+v %v", row, err)
			}
			row, err = f.store.InstanceByID(ctx, production.ID)
			if err != nil || row.State != string(state.StateWarm) {
				t.Fatalf("failed stage resume retired production: %+v %v", row, err)
			}
		})
	}
}
