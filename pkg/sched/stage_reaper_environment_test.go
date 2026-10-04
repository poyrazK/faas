// adr: 531
package sched

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func stageReaperPolicyDeployment(t *testing.T, f stageSnapshotFixture, scope string, settings state.ProjectEnvironmentWorkloadSettings) state.Deployment {
	t.Helper()
	ctx := t.Context()
	head, err := f.store.ProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, scope, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, scope, f.app.ID, head.Revision, settings); err != nil {
		t.Fatal(err)
	}
	dep, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: scope, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetDeploymentRootfs(ctx, dep.ID, "/local/"+dep.ID+".ext4", "apps/stage-reaper/"+dep.ID+".ext4", 4096); err != nil {
		t.Fatal(err)
	}
	return dep
}

func TestRunReaperKeepsIndependentPinnedEnvironmentFloors(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WarmPoolSize = 0
	settings.MinInstances = 2
	production := stageReaperPolicyDeployment(t, f, "production", settings)
	if err := f.store.MarkDeploymentSuperseded(ctx, f.prod.ID); err != nil {
		t.Fatal(err)
	}
	settings.MinInstances = 1
	stage := stageReaperPolicyDeployment(t, f, "stage", settings)
	if err := f.store.MarkDeploymentSuperseded(ctx, f.stage.ID); err != nil {
		t.Fatal(err)
	}
	// Neither a later desired head nor the compatibility App projection may
	// replace the settings attached to these running deployment generations.
	head, err := f.store.ProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings.MinInstances = 4
	if _, err := f.store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, head.Revision, settings); err != nil {
		t.Fatal(err)
	}
	sharedFloor := 5
	if _, err := f.store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{MinInstances: &sharedFloor, SetMinInstances: true}); err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	for _, set := range []struct {
		dep   state.Deployment
		count int
	}{{production, 3}, {stage, 2}} {
		for i := 0; i < set.count; i++ {
			result, err := engine.AdmitInstance(ctx, f.app.ID, set.dep.ID, set.dep.Scope, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.TouchInstancesLastSeen(ctx, []state.InstanceTouch{{InstanceID: result.InstanceID, LastRequest: time.Now().Add(-time.Hour)}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	loop := NewLoop(nil, engine, testLog())
	loop.runReaper(ctx)
	instances, err := f.store.ListInstancesForApp(ctx, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, instance := range instances {
		if instance.State == string(state.StateRunning) {
			counts[instance.DeploymentID]++
		}
	}
	if counts[production.ID] != 2 || counts[stage.ID] != 1 || vmm.snapshots != 2 {
		t.Fatalf("reaper merged stages or adopted desired settings: running=%v snapshots=%d", counts, vmm.snapshots)
	}
}

func TestRunReaperUsesPinnedEnvironmentIdleTimeout(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WarmPoolSize = 0
	settings.IdleTimeoutS = 600
	stage := stageReaperPolicyDeployment(t, f, "stage", settings)
	sharedTimeout := 10
	if _, err := f.store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{IdleTimeoutS: &sharedTimeout, SetIdleTimeout: true}); err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	instances := map[string]string{}
	for _, dep := range []state.Deployment{f.prod, stage} {
		result, err := engine.Wake(ctx, f.app.ID, dep.ID, dep.Scope, "")
		if err != nil {
			t.Fatal(err)
		}
		instances[dep.Scope] = result.InstanceID
	}
	loop := NewLoop(nil, engine, testLog()).WithClock(func() time.Time { return time.Now().Add(180 * time.Second) })
	loop.runReaper(ctx)
	production, err := f.store.InstanceByID(ctx, instances["production"])
	if err != nil {
		t.Fatal(err)
	}
	staging, err := f.store.InstanceByID(ctx, instances["stage"])
	if err != nil {
		t.Fatal(err)
	}
	if production.State != string(state.StateParked) || staging.State != string(state.StateRunning) {
		t.Fatalf("stage idle configuration affected sibling: prod=%s stage=%s", production.State, staging.State)
	}
}

func TestReaperEnvironmentSelectorsNeverCountSiblingReplicas(t *testing.T) {
	now := time.Now()
	rows := []InstanceInfo{
		{Instance: "production", AppID: "app", EnvironmentID: "production-lifetime", Scope: "production", State: state.StateRunning, Plan: api.PlanPro, MinInstances: 1, Started: now.Add(-time.Hour), LastRequest: now.Add(-time.Hour)},
		{Instance: "stage-old", AppID: "app", EnvironmentID: "stage-lifetime", Scope: "stage", State: state.StateRunning, Plan: api.PlanPro, MinInstances: 1, Started: now.Add(-time.Hour), LastRequest: now.Add(-time.Hour)},
		{Instance: "stage-new", AppID: "app", EnvironmentID: "stage-lifetime", Scope: "stage", State: state.StateRunning, Plan: api.PlanPro, MinInstances: 1, Started: now.Add(-time.Minute), LastRequest: now.Add(-time.Minute)},
	}
	ids := ReapIdle(now, rows, nil, nil)
	if !reflect.DeepEqual(ids, []string{"stage-old"}) {
		t.Fatalf("stage replicas satisfied production floor: %v", ids)
	}
	ids = ReapAggressive(now, rows, map[string]int{"app": 0}, nil, nil)
	if len(ids) != 0 {
		t.Fatalf("app load signal scaled sibling stage: %v", ids)
	}
	ids = ReapAggressive(now, rows, map[string]int{reaperEnvironmentKey(rows[1]): 0}, nil, nil)
	if !reflect.DeepEqual(ids, []string{"stage-old"}) {
		t.Fatalf("explicit stage load did not keep its own floor: %v", ids)
	}
	rows[1].EnvironmentID = "original-lifetime"
	if ids := ReapIdle(now, rows, nil, nil); len(ids) != 0 {
		t.Fatalf("recreated slug satisfied original floor: %v", ids)
	}
}

type unavailableReaperPolicyStore struct{ *state.MemStore }

func (s unavailableReaperPolicyStore) RuntimeAppValuesForDeployment(context.Context, string, string, string) (state.RuntimeAppValuesSnapshot, error) {
	return state.RuntimeAppValuesSnapshot{}, state.ErrConflict
}

func TestReaperPolicyFailureAndLostLifetimeDoNotAuthorizeScaleIn(t *testing.T) {
	for _, reason := range []string{"read-failure", "recreated-stage"} {
		t.Run(reason, func(t *testing.T) {
			f := seedStageSnapshotPolicy(t, 0, false)
			var store state.Store = f.store
			if reason == "read-failure" {
				store = unavailableReaperPolicyStore{f.store}
			} else {
				f.recreateStage(t)
			}
			loop := NewLoop(nil, newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0"), testLog())
			now := time.Now()
			rows := []InstanceInfo{{Instance: "stage", AppID: f.app.ID, DeploymentID: f.stage.ID, State: state.StateRunning, Plan: api.PlanPro, Started: now.Add(-time.Hour), LastRequest: now.Add(-time.Hour)}}
			loop.enrichReaperEnvironmentPolicies(t.Context(), []state.App{f.app}, rows, nil)
			if !rows[0].PolicyUnavailable || len(ReapIdle(now, rows, nil, nil)) != 0 || len(ReapAggressive(now, rows, map[string]int{"app": 0}, nil, nil)) != 0 {
				t.Fatalf("missing policy authorized scale-in: %+v", rows)
			}
		})
	}
}

func TestReaperPinnedWorkerPriorityAndPrewarmPolicy(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WorkloadClass = state.WorkloadClassWorker
	settings.EvictionPriority = "reserved"
	stage := stageReaperPolicyDeployment(t, f, "stage", settings)
	loop := NewLoop(nil, newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0"), testLog())
	rows := []InstanceInfo{{Instance: "production", AppID: f.app.ID, DeploymentID: f.prod.ID}, {Instance: "stage", AppID: f.app.ID, DeploymentID: stage.ID}}
	loop.enrichReaperEnvironmentPolicies(ctx, []state.App{f.app}, rows, map[string]int{f.app.ID: 3})
	sort.Slice(rows, func(i, j int) bool { return rows[i].Instance < rows[j].Instance })
	if rows[0].PrewarmMinInstances != 3 || rows[1].PrewarmMinInstances != 0 || rows[1].MinInstances != 0 || rows[1].WorkloadClass != state.WorkloadClassWorker || rows[1].EvictionPriority != "reserved" {
		t.Fatalf("reaper adopted production settings or prewarm: %+v", rows)
	}
}

func TestStageIdleParkDoesNotStampProductionScaleIn(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	if _, err := f.store.UpdateDeploymentMinInstances(ctx, f.prod.ID, 1); err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	production, err := engine.Wake(ctx, f.app.ID, f.prod.ID, "production", "")
	if err != nil {
		t.Fatal(err)
	}
	stage, err := engine.Wake(ctx, f.app.ID, f.stage.ID, "stage", "")
	if err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(nil, engine, testLog()).WithClock(func() time.Time { return time.Now().Add(180 * time.Second) })
	loop.runReaper(ctx)
	staged, err := f.store.InstanceByID(ctx, stage.InstanceID)
	if err != nil || staged.State != string(state.StateParked) {
		t.Fatalf("stage idle park failed: %+v %v", staged, err)
	}
	prod, err := f.store.InstanceByID(ctx, production.InstanceID)
	if err != nil || prod.State != string(state.StateRunning) {
		t.Fatalf("stage activity displaced production floor: %+v %v", prod, err)
	}
	app, err := f.store.AppByID(ctx, f.app.ID)
	if err != nil || app.LastScaleInAt != nil {
		t.Fatalf("stage park changed production scale-in telemetry: %+v %v", app.LastScaleInAt, err)
	}
	history, err := f.store.RuntimeScalingStateForDeployment(ctx, f.account.ID, f.app.ID, f.stage.ID)
	if err != nil || history.LastScaleInAt == nil {
		t.Fatalf("stage park did not retain its own scale-in history: %+v %v", history, err)
	}
}

type stageReaperPrewarmStore struct{ *state.MemStore }

func (s stageReaperPrewarmStore) ActivePrewarmFloor(context.Context, string, time.Time) (int, error) {
	return 3, nil
}

func TestRunReaperPrewarmAppliesAgainstPinnedProductionFloor(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	sharedFloor := 5
	if _, err := f.store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{MinInstances: &sharedFloor, SetMinInstances: true}); err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, stageReaperPrewarmStore{f.store}, vmm, &fakeNotifier{}, "1.10.0")
	productionIDs := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		wake, err := engine.AdmitInstance(ctx, f.app.ID, f.prod.ID, "production", "")
		if err != nil {
			t.Fatal(err)
		}
		productionIDs = append(productionIDs, wake.InstanceID)
	}
	staged, err := engine.Wake(ctx, f.app.ID, f.stage.ID, "stage", "")
	if err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(nil, engine, testLog()).WithClock(func() time.Time { return time.Now().Add(180 * time.Second) })
	loop.runReaper(ctx)
	for _, id := range productionIDs {
		instance, err := f.store.InstanceByID(ctx, id)
		if err != nil || instance.State != string(state.StateRunning) {
			t.Fatalf("raw desired floor hid active production prewarm: %+v %v", instance, err)
		}
	}
	instance, err := f.store.InstanceByID(ctx, staged.InstanceID)
	if err != nil || instance.State != string(state.StateParked) {
		t.Fatalf("production prewarm protected stage: %+v %v", instance, err)
	}
}

func TestReaperCachedPolicyStillChecksEveryInstanceOwner(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	account, err := f.store.CreateAccount(ctx, "foreign-reaper@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := f.store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "foreign-reaper", RAMMB: 256, MaxConcurrency: 3})
	if err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(nil, newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0"), testLog())
	rows := []InstanceInfo{{Instance: "owned", AppID: f.app.ID, DeploymentID: f.prod.ID}, {Instance: "foreign", AppID: app.ID, DeploymentID: f.prod.ID}}
	loop.enrichReaperEnvironmentPolicies(ctx, []state.App{f.app, app}, rows, nil)
	if rows[0].PolicyUnavailable || !rows[1].PolicyUnavailable {
		t.Fatalf("cached policy crossed instance ownership: %+v", rows)
	}
}
