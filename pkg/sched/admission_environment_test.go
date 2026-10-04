// adr: 567
package sched

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type foreignScalingEnvironmentStore struct{ *state.MemStore }

func (s foreignScalingEnvironmentStore) RuntimeScalingStateForDeployment(ctx context.Context, accountID, appID, deploymentID string) (state.RuntimeScalingState, error) {
	row, err := s.MemStore.RuntimeScalingStateForDeployment(ctx, accountID, appID, deploymentID)
	row.EnvironmentID = "another-lifetime"
	return row, err
}

func TestEnvironmentLedgerCountsFollowServingLifecycle(t *testing.T) {
	ledger := NewNodeLedger()
	for _, request := range []Request{
		{Instance: "stage-old-generation", DeploymentID: "old", EnvironmentKey: "environment:stage-original"},
		{Instance: "stage-new-generation", DeploymentID: "new", EnvironmentKey: "environment:stage-original"},
		{Instance: "stage-recreated", DeploymentID: "replacement", EnvironmentKey: "environment:stage-replacement"},
		{Instance: "production", DeploymentID: "prod", EnvironmentKey: "environment:production"},
		{Instance: "paused-stage", DeploymentID: "new", EnvironmentKey: "environment:stage-original", Kind: KindWarmPool},
	} {
		request.AppID, request.Plan, request.RAMMB = "app", api.PlanPro, 128
		if err := ledger.Admit(request); err != nil {
			t.Fatal(err)
		}
	}
	assert := func(stage, replacement, production, all int) {
		t.Helper()
		if got := ledger.ConcurrencyForEnvironment("app", "environment:stage-original"); got != stage {
			t.Fatalf("stage count=%d want=%d", got, stage)
		}
		if got := ledger.ConcurrencyForEnvironment("app", "environment:stage-replacement"); got != replacement {
			t.Fatalf("replacement count=%d want=%d", got, replacement)
		}
		if got := ledger.ConcurrencyForEnvironment("app", "environment:production"); got != production || ledger.Concurrency("app") != all {
			t.Fatalf("production=%d all=%d want=%d/%d", got, ledger.Concurrency("app"), production, all)
		}
	}
	assert(2, 1, 1, 4)
	resident := ledger.ResidentRAM()
	ledger.BeginSnapshot("stage-old-generation")
	ledger.BeginSnapshot("stage-old-generation")
	assert(1, 1, 1, 3)
	if ledger.ResidentRAM() != resident {
		t.Fatal("snapshot concurrency release lost resident RAM")
	}
	if !ledger.PromoteWarm("paused-stage") || ledger.PromoteWarm("paused-stage") {
		t.Fatal("warm promotion was not exactly once")
	}
	assert(2, 1, 1, 4)
	for _, id := range []string{"stage-old-generation", "stage-new-generation", "stage-recreated", "production", "paused-stage"} {
		ledger.Release(id)
		ledger.Release(id)
	}
	assert(0, 0, 0, 0)
	if ledger.ResidentRAM() != 0 || len(ledger.perAppEnvironment) != 0 {
		t.Fatalf("released lifecycle left RAM or environment counters: %d %v", ledger.ResidentRAM(), ledger.perAppEnvironment)
	}
}

func TestPropertyEnvironmentLedgerCountsMatchConcurrentAdmissions(t *testing.T) {
	ledger := NewNodeLedger()
	type admitted struct {
		instance, environment string
		err                   error
	}
	results := make(chan admitted, 60)
	for i := 0; i < 60; i++ {
		go func(i int) {
			id := fmt.Sprintf("instance-%d", i)
			environment := fmt.Sprintf("environment:%d", i%3)
			err := ledger.Admit(Request{Instance: id, AppID: "app", EnvironmentKey: environment, Plan: api.PlanScale, RAMMB: 128})
			results <- admitted{id, environment, err}
		}(i)
	}
	counts := map[string]int{}
	var accepted []admitted
	for i := 0; i < 60; i++ {
		result := <-results
		if result.err == nil {
			counts[result.environment]++
			accepted = append(accepted, result)
		} else {
			var problem *api.Problem
			if !errors.As(result.err, &problem) || problem.Code != api.CodePlanLimitConcur {
				t.Fatalf("unexpected admission failure: %v", result.err)
			}
		}
	}
	if len(accepted) != api.MustLimitsFor(api.PlanScale).MaxConcurrency || ledger.Concurrency("app") != len(accepted) {
		t.Fatal("environment accounting changed the shared plan cap")
	}
	for environment, count := range counts {
		if got := ledger.ConcurrencyForEnvironment("app", environment); got != count || ledger.ConcurrencyForEnvironment("another-app", environment) != 0 {
			t.Fatalf("concurrent serving count %s=%d want=%d", environment, got, count)
		}
	}
	var done sync.WaitGroup
	for _, row := range accepted {
		done.Add(1)
		go func(id string) {
			defer done.Done()
			ledger.BeginSnapshot(id)
			ledger.Release(id)
			ledger.Release(id)
		}(row.instance)
	}
	done.Wait()
	if ledger.Concurrency("app") != 0 || ledger.ResidentRAM() != 0 || len(ledger.perAppEnvironment) != 0 {
		t.Fatal("concurrent release left environment counts or capacity")
	}
}

func TestStageColdStartBypassesItsCooldownWhileProductionRuns(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WarmPoolSize = 0
	settings.ScalingPolicy = &state.ScalingPolicy{ScaleOutCooldownS: 600}
	stage := stageReaperPolicyDeployment(t, f, "stage", settings)
	stageReaperPolicyDeployment(t, f, "production", settings)
	engine := newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	first, err := engine.AdmitInstance(ctx, f.app.ID, "", "stage", "")
	if err != nil || first.InstanceID == "" {
		t.Fatalf("first stage admission: %+v %v", first, err)
	}
	production, err := engine.AdmitInstance(ctx, f.app.ID, "", "production", "")
	if err != nil || production.InstanceID == "" {
		t.Fatalf("production admission: %+v %v", production, err)
	}
	if err := engine.Park(ctx, first.InstanceID); err != nil {
		t.Fatal(err)
	}
	owner, err := f.store.RuntimeScalingStateForDeployment(ctx, f.account.ID, f.app.ID, stage.ID)
	key := runtimeEnvironmentAdmissionKey(owner.Scope, owner.EnvironmentID)
	if err != nil || owner.LastScaleOutAt == nil || engine.ledger.ConcurrencyForEnvironment(f.app.ID, key) != 0 || engine.ledger.Concurrency(f.app.ID) != 1 {
		t.Fatalf("test did not create a stage cold start beside production: %+v %v", owner, err)
	}
	second, err := engine.AdmitInstance(ctx, f.app.ID, "", "stage", "")
	if err != nil || second.InstanceID == "" || second.AtCapacity {
		t.Fatalf("production concurrency held a stage cold start: %+v %v", second, err)
	}
	prod, err := f.store.InstanceByID(ctx, production.InstanceID)
	if err != nil || prod.State != string(state.StateRunning) {
		t.Fatalf("stage cold start displaced production: %+v %v", prod, err)
	}
}

func TestStageNoSignalFloorCountsItsOwnReplicas(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WarmPoolSize = 0
	settings.ScalingPolicy = &state.ScalingPolicy{MinInstances: 1}
	stageReaperPolicyDeployment(t, f, "stage", settings)
	stageReaperPolicyDeployment(t, f, "production", settings)
	engine := newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	for _, scope := range []string{"production", "stage"} {
		result, err := engine.AdmitInstance(ctx, f.app.ID, "", scope, "")
		if err != nil || result.InstanceID == "" {
			t.Fatalf("sibling floor blocked %s: %+v %v", scope, result, err)
		}
	}
	result, err := engine.AdmitInstance(ctx, f.app.ID, "", "stage", "")
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodePlanLimitConcur || result.InstanceID != "" || engine.ledger.Concurrency(f.app.ID) != 2 {
		t.Fatalf("stage ignored its own no-signal floor: %+v %v", result, err)
	}
}

func TestSeedLedgerRebuildsPinnedEnvironmentAndDeploymentCounters(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WarmPoolSize = 0
	settings.CPUMillicores = 500
	production := stageReaperPolicyDeployment(t, f, "production", settings)
	settings.CPUMillicores = 1000
	stage := stageReaperPolicyDeployment(t, f, "stage", settings)
	newStage := stageReaperPolicyDeployment(t, f, "stage", settings)
	engine := newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	for _, row := range []struct {
		dep    state.Deployment
		status state.State
	}{
		{production, state.StateRunning}, {stage, state.StateRunning}, {newStage, state.StateRunning}, {stage, state.StateSnapshotting}, {newStage, state.StateWarm},
	} {
		if _, err := f.store.CreateInstance(ctx, f.app.ID, row.dep.ID, string(row.status), 256, engine.defaultLocalNodeID, ""); err != nil {
			t.Fatal(err)
		}
	}
	// The raw production projection cannot replace a deployed CPU policy or
	// cause recovery to omit already-resident capacity after a cap reduction.
	sharedCPU, sharedCap := 2000, 1
	if _, err := f.store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{CPUMillicores: &sharedCPU, MaxConcurrency: &sharedCap}); err != nil {
		t.Fatal(err)
	}
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	prodOwner, err := f.store.RuntimeScalingStateForDeployment(ctx, f.account.ID, f.app.ID, production.ID)
	if err != nil {
		t.Fatal(err)
	}
	stageOwner, err := f.store.RuntimeScalingStateForDeployment(ctx, f.account.ID, f.app.ID, stage.ID)
	if err != nil {
		t.Fatal(err)
	}
	ledger := engine.ledger
	if ledger.Concurrency(f.app.ID) != 3 || ledger.ConcurrencyForEnvironment(f.app.ID, runtimeEnvironmentAdmissionKey(prodOwner.Scope, prodOwner.EnvironmentID)) != 1 || ledger.ConcurrencyForEnvironment(f.app.ID, runtimeEnvironmentAdmissionKey(stageOwner.Scope, stageOwner.EnvironmentID)) != 2 || ledger.ConcurrencyForDeployment(f.app.ID, stage.ID) != 1 || ledger.ConcurrencyForDeployment(f.app.ID, newStage.ID) != 1 || ledger.ResidentRAM() != 5*(256+api.PerVMOverheadMB) || ledger.UsedCPUMillicoresForNode(engine.defaultLocalNodeID) != 4500 {
		t.Fatalf("recovery lost original policy/counts: all=%d stage=%d new=%d ram=%d cpu=%d", ledger.Concurrency(f.app.ID), ledger.ConcurrencyForDeployment(f.app.ID, stage.ID), ledger.ConcurrencyForDeployment(f.app.ID, newStage.ID), ledger.ResidentRAM(), ledger.UsedCPUMillicoresForNode(engine.defaultLocalNodeID))
	}
}

func TestSeedLedgerLostStageLifetimeRetainsOrphanCapacity(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	if _, err := f.store.CreateInstance(ctx, f.app.ID, f.stage.ID, string(state.StateRunning), 256, state.DefaultLocalNodeName, ""); err != nil {
		t.Fatal(err)
	}
	f.recreateStage(t.Context(), t)
	engine := newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	replacement, err := f.store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if engine.ledger.ConcurrencyForEnvironment(f.app.ID, "deployment:"+f.stage.ID) != 1 || engine.ledger.ConcurrencyForEnvironment(f.app.ID, "environment:"+replacement.ID) != 0 || engine.ledger.ResidentRAM() != 256+api.PerVMOverheadMB {
		t.Fatal("recovery dropped orphan RAM or assigned it to replacement stage")
	}
}

func TestSeedLedgerDoesNotDowngradeTransientOwnershipFailure(t *testing.T) {
	f := seedStageSnapshotPolicy(t, 0, false)
	if _, err := f.store.CreateInstance(t.Context(), f.app.ID, f.stage.ID, string(state.StateRunning), 256, state.DefaultLocalNodeName, ""); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, invalidScalingStateStore{MemStore: f.store}, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := engine.SeedLedger(t.Context()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("transient ownership failure adopted a fallback: %v", err)
	}
}

func TestEnvironmentLedgerRecoveryRecordsOverCapServingRows(t *testing.T) {
	ledger := NewNodeLedger()
	for i, id := range []string{"first", "second", "third"} {
		request := Request{Instance: id, AppID: "app", EnvironmentKey: "environment:stage", Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1, AllowConcurrencyRecovery: true}
		if err := ledger.Admit(request); err != nil {
			t.Fatalf("recovery omitted resident %d: %v", i, err)
		}
	}
	if err := ledger.Admit(Request{Instance: "new", AppID: "app", EnvironmentKey: "environment:stage", Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1}); err == nil {
		t.Fatal("recovered over-cap capacity authorized a new VM")
	}
	if ledger.ConcurrencyForEnvironment("app", "environment:stage") != 3 || ledger.ResidentRAM() != 3*(128+api.PerVMOverheadMB) {
		t.Fatal("recovery hid over-cap serving capacity")
	}
	if err := ledger.Admit(Request{Instance: "production", AppID: "app", Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1}); err != nil {
		t.Fatalf("stage's configured over-cap debt constrained production below the shared plan budget: %v", err)
	}
}

func TestReaperRejectsScalingHistoryFromDifferentEnvironmentLifetime(t *testing.T) {
	f := seedStageSnapshotPolicy(t, 0, false)
	store := foreignScalingEnvironmentStore{f.store}
	loop := NewLoop(nil, newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0"), testLog())
	now := time.Now()
	rows := []InstanceInfo{{Instance: "stage", AppID: f.app.ID, DeploymentID: f.stage.ID, State: state.StateRunning, Plan: api.PlanPro, Started: now.Add(-time.Hour), LastRequest: now.Add(-time.Hour)}}
	loop.enrichReaperEnvironmentPolicies(t.Context(), []state.App{f.app}, rows, nil)
	if !rows[0].PolicyUnavailable || len(ReapIdle(now, rows, nil, nil)) != 0 {
		t.Fatalf("different lifetime's clock authorized reaping: %+v", rows)
	}
}

func TestStageAdmissionRejectsScalingHistoryFromDifferentLifetime(t *testing.T) {
	f := seedStageSnapshotPolicy(t, 0, false)
	engine := newEngine(t, foreignScalingEnvironmentStore{f.store}, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	result, err := engine.AdmitInstance(t.Context(), f.app.ID, f.stage.ID, "stage", "")
	if !errors.Is(err, state.ErrConflict) || result.InstanceID != "" || engine.ledger.ResidentRAM() != 0 {
		t.Fatalf("different lifetime's clock authorized admission: %+v %v", result, err)
	}
}
