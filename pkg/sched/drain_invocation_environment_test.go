// adr: 566
package sched

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestDrain_StageInvocationWakesPinnedStageDeployment(t *testing.T) {
	testDrainStageInvocation(t, false, false)
}

func TestDrain_StageKeyedInvocationWakesPinnedStageDeployment(t *testing.T) {
	testDrainStageInvocation(t, true, false)
}

func TestDrain_StageDelayedTaskIgnoresProductionQueueTrigger(t *testing.T) {
	testDrainStageInvocation(t, false, true)
}

func testDrainStageInvocation(t *testing.T, keyed, productionTrigger bool) {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "stage-drain@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "stage-drain"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "stage-drain-api", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 4, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	policy := workpolicy.Policy{Name: "stage-work", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest,
		MaxRunningPerFairnessKey: 1, ExpiresAfter: time.Hour}
	if keyed {
		if _, _, err := state.UpsertEnvironmentWorkPolicy(ctx, store, app, "staging", nil, policy); err != nil {
			t.Fatal(err)
		}
	}
	var stage state.Deployment
	for _, scope := range []string{"production", "staging"} {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, scope, 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}}); err != nil {
			t.Fatal(err)
		}
		if scope == "staging" {
			stage = dep
		}
	}
	source := state.InvocationAsyncInvoke
	var production state.Invocation
	if productionTrigger {
		source = state.InvocationDelayedTask
		if _, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "queue", "timers", true, []byte(`{"mode":"delayed_task"}`),
			"delayed_task", 1, 20, 3, 8192, "commit", api.MustLimitsFor(api.PlanPro)); err != nil {
			t.Fatal(err)
		}
		production, err = store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: source, DueAt: time.Now().Add(-time.Second)})
		if err != nil {
			t.Fatal(err)
		}
	}
	prepared, _, err := state.ResolveInvocationVersionForEnvironment(ctx, store, state.Invocation{AppID: app.ID, AccountID: account.ID,
		Source: source, Method: "POST", Path: "/work", Payload: json.RawMessage(`{}`), DueAt: time.Now().Add(-time.Second)}, "staging")
	if err != nil {
		t.Fatal(err)
	}
	var inv state.Invocation
	if keyed {
		inv, err = store.EnqueueKeyedInvocation(ctx, prepared, policy, "s:one", "s:customer")
	} else {
		inv, err = store.EnqueueInvocation(ctx, prepared)
	}
	if err != nil {
		t.Fatal(err)
	}
	if keyed {
		if _, err := state.DeleteEnvironmentWorkPolicy(ctx, store, app, "staging", policy.Name, nil); err != nil {
			t.Fatal(err)
		}
	}
	vmm, notifier, synth := &fakeVMM{}, &fakeNotifier{}, &drainSynth{}
	engine := newEngine(t, store, vmm, notifier, "1.10.0")
	drain := NewDrain(store, engine, WithDrainGatewaySynth(synth))
	drain.Tick(ctx)
	completed, err := store.InvocationByID(ctx, inv.ID)
	if err != nil || completed.State != state.InvocationCompleted || completed.InstanceID == "" || synth.calls.Load() != 1 {
		t.Fatalf("stage dispatch = %+v, calls=%d, %v", completed, synth.calls.Load(), err)
	}
	instance, err := store.InstanceByID(ctx, completed.InstanceID)
	if err != nil || instance.DeploymentID != stage.ID {
		t.Fatalf("stage invocation woke production: %+v, %v", instance, err)
	}
	if productionTrigger {
		got, err := store.InvocationByID(ctx, production.ID)
		if err != nil || got.State != state.InvocationPending || got.Attempts != 0 {
			t.Fatalf("stage drain took production trigger work: %+v, %v", got, err)
		}
	}
}
