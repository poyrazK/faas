// adr: 585
package sched

import (
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentLedgerEnforcesConfiguredAndSharedCeilings(t *testing.T) {
	ledger := NewNodeLedger()
	admit := func(id, key string, production bool, ceiling int) error {
		return ledger.Admit(Request{Instance: id, AppID: "app", EnvironmentKey: key, ProductionEnvironment: production, Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: ceiling})
	}
	if err := admit("prod", "environment:production", true, 1); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"stage-one", "stage-two"} {
		if err := admit(id, "environment:stage", false, 2); err != nil {
			t.Fatalf("production's ceiling constrained stage: %v", err)
		}
	}
	for _, row := range []struct {
		id, key    string
		production bool
		ceiling    int
	}{
		{"prod-extra", "environment:production", true, 1},
		{"stage-extra", "environment:stage", false, 2},
	} {
		assertServingCapacityRefusal(t, admit(row.id, row.key, row.production, row.ceiling), row.ceiling, row.ceiling)
	}
	if ledger.Concurrency("app") != 3 || ledger.ConcurrencyForEnvironment("app", "environment:stage") != 2 {
		t.Fatal("configured refusal changed serving counts")
	}
	for _, id := range []string{"other-one", "other-two"} {
		if err := admit(id, "environment:other", false, 5); err != nil {
			t.Fatal(err)
		}
	}
	before := ledger.ResidentRAM()
	assertServingCapacityRefusal(t, admit("other-extra", "environment:other", false, 5), 5, 5)
	if ledger.ResidentRAM() != before || ledger.Concurrency("app") != 5 {
		t.Fatal("shared-plan refusal reserved capacity")
	}
}

func assertServingCapacityRefusal(t *testing.T, err error, limit, observed int) {
	t.Helper()
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodePlanLimitConcur || problem.Limit == nil || problem.Observed == nil || *problem.Limit != int64(limit) || *problem.Observed != int64(observed) {
		t.Fatalf("wrong capacity refusal: %v want limit=%d observed=%d", err, limit, observed)
	}
}

func TestEnvironmentLedgerOverlapNeedsSameEnvironmentAndKeepsSharedBound(t *testing.T) {
	ledger := NewNodeLedger()
	for i := 0; i < 4; i++ {
		if err := ledger.Admit(Request{Instance: fmt.Sprintf("prod-%d", i), AppID: "app", EnvironmentKey: "environment:production", ProductionEnvironment: true, Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 5}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ledger.Admit(Request{Instance: "stage-old", AppID: "app", EnvironmentKey: "environment:stage", Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	assertServingCapacityRefusal(t, ledger.Admit(Request{Instance: "new-sibling", AppID: "app", EnvironmentKey: "environment:new", Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 5, AllowConcurrencyOverlap: true}), 5, 5)
	if err := ledger.Admit(Request{Instance: "stage-canary", AppID: "app", EnvironmentKey: "environment:stage", Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1, AllowConcurrencyOverlap: true}); err != nil {
		t.Fatalf("same-stage rollout rejected: %v", err)
	}
	assertServingCapacityRefusal(t, ledger.Admit(Request{Instance: "stage-canary-extra", AppID: "app", EnvironmentKey: "environment:stage", Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1, AllowConcurrencyOverlap: true}), 2, 2)
	capacity := ledger.servingCapacity("app", "environment:production", true, 5, api.MustLimitsFor(api.PlanPro), true)
	if limit, have, refused := capacity.refusal(); !refused || limit != 6 || have != 6 {
		t.Fatalf("wrong shared overlap boundary: %+v", capacity)
	}
	// The public error keeps the steady plan budget, even while an admitted
	// rollout temporarily occupies its one extra slot.
	assertServingCapacityRefusal(t, ledger.Admit(Request{Instance: "prod-canary", AppID: "app", EnvironmentKey: "environment:production", ProductionEnvironment: true, Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 5, AllowConcurrencyOverlap: true}), 5, 6)
	if ledger.Concurrency("app") != 6 {
		t.Fatal("rollouts exceeded shared max+1 bound")
	}
}

func TestEnvironmentLedgerBridgesOnlyLegacyProduction(t *testing.T) {
	ledger := NewNodeLedger()
	if err := ledger.Admit(Request{Instance: "legacy", AppID: "app", Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Admit(Request{Instance: "pinned", AppID: "app", EnvironmentKey: "environment:production", ProductionEnvironment: true, Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1, AllowConcurrencyOverlap: true}); err != nil {
		t.Fatal(err)
	}
	assertServingCapacityRefusal(t, ledger.Admit(Request{Instance: "legacy-extra", AppID: "app", Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1}), 1, 2)
	assertServingCapacityRefusal(t, ledger.Admit(Request{Instance: "pinned-extra", AppID: "app", EnvironmentKey: "environment:production", ProductionEnvironment: true, Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1}), 1, 2)
	if err := ledger.Admit(Request{Instance: "stage", AppID: "app", EnvironmentKey: "environment:stage", Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1}); err != nil {
		t.Fatalf("legacy production counted toward stage ceiling: %v", err)
	}
	for _, id := range []string{"legacy", "pinned", "stage"} {
		ledger.Release(id)
	}
	if len(ledger.perAppProduction) != 0 || ledger.ResidentRAM() != 0 {
		t.Fatal("production compatibility index leaked")
	}
}

func stageCapacitySettings(t *testing.T, f stageSnapshotFixture, scope string, ceiling, warm int) state.Deployment {
	t.Helper()
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	settings.MaxConcurrency, settings.WarmPoolSize = ceiling, warm
	return stageReaperPolicyDeployment(t, f, scope, settings)
}

func TestNativeStageCapacityUsesDeployedEnvironmentCeiling(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	production := stageCapacitySettings(t, f, "production", 1, 0)
	stage := stageCapacitySettings(t, f, "stage", 2, 0)
	engine := newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	for _, scope := range []string{"production", "stage", "stage"} {
		result, err := engine.AdmitInstance(ctx, f.app.ID, "", scope, "")
		if err != nil || result.AtCapacity || result.InstanceID == "" {
			t.Fatalf("%s admission: %+v %v", scope, result, err)
		}
	}
	// Neither an edited desired head nor a shared App compatibility setting
	// replaces the deployed stage's configured limit.
	head, err := f.store.ProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings := head.Settings
	settings.MaxConcurrency = 4
	if _, err := f.store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, head.Revision, settings); err != nil {
		t.Fatal(err)
	}
	sharedCeiling := 1
	if _, err := f.store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{MaxConcurrency: &sharedCeiling}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"production", "stage"} {
		result, err := engine.AdmitInstance(ctx, f.app.ID, "", scope, "")
		if err != nil || !result.AtCapacity || result.InstanceID != "" {
			t.Fatalf("%s ignored deployed ceiling: %+v %v", scope, result, err)
		}
	}
	if engine.ledger.ConcurrencyForDeployment(f.app.ID, production.ID) != 1 || engine.ledger.ConcurrencyForDeployment(f.app.ID, stage.ID) != 2 || engine.ledger.Concurrency(f.app.ID) != 3 {
		t.Fatal("scope limits changed sibling capacity")
	}
}

func TestNativeSiblingStageCannotBorrowRolloutGrant(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	stage := stageCapacitySettings(t, f, "stage", 5, 0)
	stageCapacitySettings(t, f, "production", 5, 0)
	engine := newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	for i := 0; i < 5; i++ {
		result, err := engine.AdmitInstance(ctx, f.app.ID, "", "production", "")
		if err != nil || result.InstanceID == "" || result.AtCapacity {
			t.Fatalf("production admission: %+v %v", result, err)
		}
	}
	for _, route := range []struct{ deployment, trigger string }{{"", ""}, {stage.ID, TriggerDeploymentSmoke}} {
		result, err := engine.AdmitInstance(ctx, f.app.ID, route.deployment, "stage", route.trigger)
		if err != nil || !result.AtCapacity || result.InstanceID != "" {
			t.Fatalf("sibling borrowed rollout capacity: %+v %v", result, err)
		}
	}
	if engine.ledger.Concurrency(f.app.ID) != 5 || engine.ledger.ConcurrencyForDeployment(f.app.ID, stage.ID) != 0 {
		t.Fatal("sibling consumed production's rollout allowance")
	}
}

func TestNativeSameStageRolloutUsesOneSharedAllowance(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	stageCapacitySettings(t, f, "production", 5, 0)
	stageCapacitySettings(t, f, "stage", 1, 0)
	engine := newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	for _, scope := range []string{"production", "production", "production", "production", "stage"} {
		result, err := engine.AdmitInstance(ctx, f.app.ID, "", scope, "")
		if err != nil || result.InstanceID == "" || result.AtCapacity {
			t.Fatalf("initial %s admission: %+v %v", scope, result, err)
		}
	}
	candidate := stageCapacitySettings(t, f, "stage", 1, 0)
	result, err := engine.AdmitInstance(ctx, f.app.ID, "", "stage", "")
	if err != nil || result.InstanceID == "" || result.AtCapacity || result.DeploymentID != candidate.ID {
		t.Fatalf("same-stage rollout failed: %+v %v", result, err)
	}
	result, err = engine.AdmitInstance(ctx, f.app.ID, candidate.ID, "stage", TriggerDeploymentSmoke)
	if err != nil || !result.AtCapacity || engine.ledger.Concurrency(f.app.ID) != 6 {
		t.Fatalf("rollout exceeded one shared allowance: %+v %v", result, err)
	}
}

func TestNativeStageWarmPromotionUsesItsOwnCeiling(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	stageCapacitySettings(t, f, "production", 1, 0)
	stage := stageCapacitySettings(t, f, "stage", 2, 1)
	vmm := &warmResumeFakeVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	production, err := engine.AdmitInstance(ctx, f.app.ID, "", "production", "")
	if err != nil || production.InstanceID == "" {
		t.Fatal(err)
	}
	warm := stagePoolInstance(t, f, stage)
	if err := f.store.SetInstanceRuntime(ctx, warm.ID, "fc-stage-warm", "10.100.0.2", 20001); err != nil {
		t.Fatal(err)
	}
	result, err := engine.AdmitInstance(ctx, f.app.ID, "", "stage", "")
	if err != nil || result.InstanceID != warm.ID || vmm.resumeCalls != 1 {
		t.Fatalf("production ceiling held stage warm promotion: %+v %v", result, err)
	}
	owner, err := f.store.RuntimeScalingStateForDeployment(ctx, f.account.ID, f.app.ID, stage.ID)
	if err != nil || engine.ledger.ConcurrencyForEnvironment(f.app.ID, runtimeEnvironmentAdmissionKey(owner.Scope, owner.EnvironmentID)) != 1 || engine.ledger.servingEnvironmentConcurrency(f.app.ID, "", true) != 1 {
		t.Fatalf("warm promotion merged serving counts: %+v %v", owner, err)
	}
}

func TestNativeStageCeilingHoldsItsWarmPoolWithoutHoldingProduction(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	stageCapacitySettings(t, f, "production", 3, 0)
	stage := stageCapacitySettings(t, f, "stage", 1, 1)
	vmm := &warmResumeFakeVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	first, err := engine.AdmitInstance(ctx, f.app.ID, "", "stage", "")
	if err != nil || first.InstanceID == "" {
		t.Fatal(err)
	}
	warm := stagePoolInstance(t, f, stage)
	if err := f.store.SetInstanceRuntime(ctx, warm.ID, "fc-stage-held", "10.100.0.2", 20001); err != nil {
		t.Fatal(err)
	}
	result, err := engine.AdmitInstance(ctx, f.app.ID, "", "stage", "")
	if err != nil || !result.AtCapacity || result.InstanceID != "" || vmm.resumeCalls != 0 {
		t.Fatalf("stage ceiling resumed extra paused capacity: %+v %v", result, err)
	}
	row, err := f.store.InstanceByID(ctx, warm.ID)
	if err != nil || row.State != string(state.StateWarm) {
		t.Fatalf("capacity hold reclaimed paused stage: %+v %v", row, err)
	}
	result, err = engine.AdmitInstance(ctx, f.app.ID, "", "production", "")
	if err != nil || result.InstanceID == "" || result.AtCapacity {
		t.Fatalf("stage ceiling held production: %+v %v", result, err)
	}
}

func TestNativeProductionRolloutBridgesUnpinnedLegacyCapacity(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	app, err := f.store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "legacy-capacity", WorkloadName: "legacy-capacity", RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetDeploymentRootfs(ctx, legacy.ID, "/local/legacy.ext4", "apps/legacy/legacy.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	first, err := engine.AdmitInstance(ctx, app.ID, "", "default", "")
	if err != nil || first.InstanceID == "" {
		t.Fatalf("legacy admission: %+v %v", first, err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "production", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	candidate, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetDeploymentRootfs(ctx, candidate.ID, "/local/pinned.ext4", "apps/legacy/pinned.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	result, err := engine.AdmitInstance(ctx, app.ID, "", "production", "")
	if err != nil || result.InstanceID == "" || result.DeploymentID != candidate.ID {
		t.Fatalf("legacy capacity did not allow pinned rollout: %+v %v", result, err)
	}
	for _, scope := range []string{"default", "production"} {
		result, err = engine.AdmitInstance(ctx, app.ID, "", scope, "")
		if err != nil || !result.AtCapacity || engine.ledger.Concurrency(app.ID) != 2 {
			t.Fatalf("production aliases bypassed shared configured limit: %+v %v", result, err)
		}
	}
}
