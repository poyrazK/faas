// adr: 567 — environment intent and runtime ownership contracts.
package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestWorkerAccountCapacityPrecedesVMAdmissionAndPrime(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, deps := seedWorkerScopes(t, store, api.PlanHobby)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	first, err := engine.AdmitInstanceForDeployment(ctx, app.ID, deps["default"].ID, "default", TriggerWorkerPool)
	if err != nil || first.AtCapacity || first.InstanceID == "" {
		t.Fatalf("first worker admission = %+v, %v", first, err)
	}
	second, err := engine.AdmitInstanceForDeployment(ctx, app.ID, deps["staging"].ID, "staging", TriggerWorkerPool)
	if err != nil || !second.AtCapacity || second.InstanceID != "" {
		t.Fatalf("account-full worker admission = %+v, %v", second, err)
	}
	if err := store.UpdateDeploymentStatus(ctx, deps["staging"].ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	err = engine.Prime(ctx, app.ID, deps["staging"].ID)
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeCapacity {
		t.Fatalf("account-full prime = %v", err)
	}
	rows, err := store.ListInstancesForApp(ctx, app.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != first.InstanceID || vmm.coldBoots != 1 || vmm.restores != 0 {
		t.Fatalf("quota denial changed fleet: rows=%d boots=%d restores=%d err=%v", len(rows), vmm.coldBoots, vmm.restores, err)
	}
}

func TestWorkerScopedAccountCapacityReportsBlockedDemand(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, app, _ := seedWorkerScopes(t, store, api.PlanHobby)
	for _, scope := range []string{"default", "staging"} {
		enqueueScopedDemand(t, store, acct, app, scope, "", 1)
	}
	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	for turn := 1; turn <= 2; turn++ {
		if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
			t.Fatal(err)
		}
		if got := scopedWorkerCounter(t, ops, "schedd_scale_up_decisions_total", map[string]string{"app": app.ID, "outcome": "reject_at_cap"}); got != float64(turn) {
			t.Fatalf("blocked demand counter = %v, want %d", got, turn)
		}
		if got := scopedWorkerCounter(t, ops, "schedd_scale_up_decisions_total", map[string]string{"app": app.ID, "outcome": "admit"}); got != 1 {
			t.Fatalf("actual admission counter = %v, want one", got)
		}
	}
	if vmm.coldBoots != 1 {
		t.Fatalf("account-full environment booted another VM: %d", vmm.coldBoots)
	}
}
