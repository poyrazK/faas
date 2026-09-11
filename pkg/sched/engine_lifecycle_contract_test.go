// spec: §6.2

package sched

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func deploymentWithMissingSecret(t *testing.T, store state.Store, appID string) state.Deployment {
	t.Helper()
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:              appID,
		Kind:               state.DeploymentKindImage,
		ImageDigest:        "sha256:missing-secret-test",
		Status:             state.DeployLive,
		OverrideEnvSecrets: json.RawMessage(`{"MISSING":"secret:MISSING"}`),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return dep
}

func TestWakeSealedEnvFailureReleasesAdmissionImmediately(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanHobby, 256, 1)
	dep := deploymentWithMissingSecret(t, store, app.ID)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	if _, err := e.Wake(context.Background(), app.ID, dep.ID, "", ""); err == nil {
		t.Fatal("Wake returned nil, want missing-secret error")
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Fatalf("resident RAM = %d, want 0 after sealed-env rejection", got)
	}
	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(instances) != 1 || instances[0].State != string(state.StateFailed) {
		t.Fatalf("instances after sealed-env rejection = %+v, want one failed row", instances)
	}
}

func TestPrimeSealedEnvFailureReleasesAdmissionImmediately(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanHobby, 256, 1)
	dep := deploymentWithMissingSecret(t, store, app.ID)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	if err := e.Prime(context.Background(), app.ID, dep.ID); err == nil {
		t.Fatal("Prime returned nil, want missing-secret error")
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Fatalf("resident RAM = %d, want 0 after sealed-env rejection", got)
	}
	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(instances) != 1 || instances[0].State != string(state.StateFailed) {
		t.Fatalf("instances after sealed-env rejection = %+v, want one failed row", instances)
	}
}

func TestPrimeAllowsWarmReplacementAtServingConcurrencyCap(t *testing.T) {
	store := state.NewMemStore()
	acct, app, oldDep := seedApp(t, store, api.PlanScale, 1024, 1)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		t.Fatalf("LimitsFor(%q) failed", acct.Plan)
	}

	old, err := store.CreateInstance(context.Background(), app.ID, oldDep.ID,
		string(state.StateRunning), app.RAMMB, e.defaultLocalNodeID, "old-live-wake")
	if err != nil {
		t.Fatalf("CreateInstance(old live): %v", err)
	}
	if err := e.Ledger().Admit(Request{
		Instance: old.ID, AppID: app.ID, DeploymentID: oldDep.ID,
		Plan: acct.Plan, RAMMB: app.RAMMB, VCPU: limits.VCPU,
		MaxConcurrency: app.MaxConcurrency, NodeID: e.defaultLocalNodeID,
	}); err != nil {
		t.Fatalf("admit old live instance: %v", err)
	}

	replacement, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:replacement", Status: state.DeploySnapshotting,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(replacement): %v", err)
	}
	if err := e.Prime(context.Background(), app.ID, replacement.ID); err != nil {
		t.Fatalf("Prime replacement at max_concurrency=1: %v", err)
	}

	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("instances after replacement prime = %+v, want old live + new parked", instances)
	}
	var oldState, replacementState string
	for _, instance := range instances {
		if instance.ID == old.ID {
			oldState = instance.State
		}
		if instance.DeploymentID == replacement.ID {
			replacementState = instance.State
		}
	}
	if oldState != string(state.StateRunning) {
		t.Errorf("old revision state = %q, want running", oldState)
	}
	if replacementState != string(state.StateParked) {
		t.Errorf("replacement revision state = %q, want parked", replacementState)
	}
	if got := e.Ledger().Concurrency(app.ID); got != 1 {
		t.Errorf("serving concurrency after prime = %d, want 1 for the old revision", got)
	}
}
