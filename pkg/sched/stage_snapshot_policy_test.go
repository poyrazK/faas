// adr: 568
package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type stageSnapshotFixture struct {
	store   *state.MemStore
	account state.Account
	project state.Project
	app     state.App
	prod    state.Deployment
	stage   state.Deployment
}

func seedStageSnapshotPolicy(t *testing.T, productionPool int, warmSnapshots bool) stageSnapshotFixture {
	t.Helper()
	ctx := t.Context()
	f := stageSnapshotFixture{store: state.NewMemStore()}
	var err error
	f.account, err = f.store.CreateAccount(ctx, "stage-snapshot@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.project, err = f.store.CreateProject(ctx, state.Project{AccountID: f.account.ID, Slug: "stage-snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = f.store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage-snapshot", RAMMB: 256,
		MaxConcurrency: 5, IdleTimeoutS: 60, WarmPoolSize: 2, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WarmPoolSize = productionPool
	if _, err := f.store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "production", f.app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	settings.WarmPoolSize, settings.WarmSnapshotEnabled, settings.WarmSnapshotMinRequests = 3, warmSnapshots, 1
	if _, err := f.store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	for scope, target := range map[string]*state.Deployment{"production": &f.prod, "stage": &f.stage} {
		*target, err = f.store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: scope, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + scope, Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.SetDeploymentRootfs(ctx, target.ID, "/local/"+scope+".ext4", "apps/stage-snapshot/"+scope+".ext4", 4096); err != nil {
			t.Fatal(err)
		}
		*target, err = f.store.DeploymentByID(ctx, target.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f stageSnapshotFixture) recreateStage(ctx context.Context, t *testing.T) {
	t.Helper()
	if err := f.store.MarkDeploymentSuperseded(ctx, f.stage.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpsertAppSecretWithClassInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", "key", "1111111111111111", state.SecretClassEphemeral, []byte("replacement-sealed")); err != nil {
		t.Fatal(err)
	}
}

type stagePolicySnapshotVMM struct {
	*fakeVMM
	initHook func()
}

func (v *stagePolicySnapshotVMM) PauseAndSnapshot(ctx context.Context, node, instance, vmstate, memKey, stateKey string, beforeCheckpoint bool) (SnapshotBytes, error) {
	if v.initHook != nil {
		v.initHook()
	}
	return v.fakeVMM.PauseAndSnapshot(ctx, node, instance, vmstate, memKey, stateKey, beforeCheckpoint)
}

func TestStageParkRejectsRecreatedEnvironment(t *testing.T) {
	for _, moment := range []string{"before-park", "during-warm", "during-init"} {
		t.Run(moment, func(t *testing.T) {
			ctx := t.Context()
			f := seedStageSnapshotPolicy(t, 0, moment != "during-init")
			vmm := &stagePolicySnapshotVMM{fakeVMM: &fakeVMM{}}
			notifier := &fakeNotifier{}
			engine := newEngine(t, f.store, vmm, notifier, "1.10.0")
			ins, err := f.store.CreateInstance(ctx, f.app.ID, f.stage.ID, string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.SetInstanceFrameworkReadyAt(ctx, ins.ID, time.Now().Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.TouchInstancesWithRequestDelta(ctx, []state.InstanceTouch{{InstanceID: ins.ID, LastRequest: time.Now(), RequestDelta: 1}}); err != nil {
				t.Fatal(err)
			}
			if err := f.store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "production", "TOKEN", []byte("production-sealed")); err != nil {
				t.Fatal(err)
			}
			if moment == "before-park" {
				f.recreateStage(t.Context(), t)
			} else if moment == "during-warm" {
				vmm.warmSnapshotHook = func() { f.recreateStage(t.Context(), t) }
			} else {
				vmm.initHook = func() { f.recreateStage(t.Context(), t) }
			}
			if err := engine.Park(ctx, ins.ID); err != nil {
				t.Fatal(err)
			}
			if notifier.count(db.NotifySnapshotWritten) != 0 {
				t.Fatal("old stage published snapshot after environment replacement")
			}
			current, err := f.store.InstanceByID(ctx, ins.ID)
			if err != nil || state.IsLive(current.State) {
				t.Fatalf("old stage retained running resources: %+v %v", current, err)
			}
			if moment == "before-park" && (vmm.snapshots != 0 || vmm.warmSnapshots != 0 || vmm.destroys != 1) {
				t.Fatalf("recreated stage reached snapshot capture: init=%d warm=%d destroy=%d", vmm.snapshots, vmm.warmSnapshots, vmm.destroys)
			}
			if moment == "during-warm" && vmm.warmSnapshots != 1 {
				t.Fatal("test did not enter warm capture")
			}
			if moment == "during-init" && vmm.snapshots != 1 {
				t.Fatal("test did not enter init capture")
			}
			for scope, cipher := range map[string]string{"production": "production-sealed", "stage": "replacement-sealed"} {
				row, err := f.store.GetAppSecretInScope(ctx, f.account.ID, f.app.ID, scope, "TOKEN")
				if err != nil || string(row.Ciphertext) != cipher {
					t.Fatalf("snapshot policy changed %s secrets: %+v %v", scope, row, err)
				}
			}
		})
	}
}
