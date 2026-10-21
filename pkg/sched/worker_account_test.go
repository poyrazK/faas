// adr: 568 — environment intent and runtime ownership contracts.
package sched_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestWorkerAccountCapacityPrecedesVMAdmissionAndPrime(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, deps := sched.SeedWorkerScopes(t, store, api.PlanHobby)
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	first, err := engine.AdmitInstanceForDeployment(ctx, app.ID, deps["default"].ID, "default", sched.TriggerWorkerPool)
	if err != nil || first.AtCapacity || first.InstanceID == "" {
		t.Fatalf("first worker admission = %+v, %v", first, err)
	}
	second, err := engine.AdmitInstanceForDeployment(ctx, app.ID, deps["staging"].ID, "staging", sched.TriggerWorkerPool)
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
	if err != nil || len(rows) != 1 || rows[0].ID != first.InstanceID || (*vmm.ColdBoots()) != 1 || (*vmm.Restores()) != 0 {
		t.Fatalf("quota denial changed fleet: rows=%d boots=%d restores=%d err=%v", len(rows), (*vmm.ColdBoots()), (*vmm.Restores()), err)
	}
}

func TestWorkerScopedAccountCapacityReportsBlockedDemand(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, app, _ := sched.SeedWorkerScopes(t, store, api.PlanHobby)
	for _, scope := range []string{"default", "staging"} {
		sched.EnqueueScopedDemand(t, store, acct, app, scope, "", 1)
	}
	vmm := &sched.FakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	for turn := 1; turn <= 2; turn++ {
		if err := engine.ReconcileWorkerPools(ctx, app.ID, sched.TriggerWorkerPool); err != nil {
			t.Fatal(err)
		}
		engine.WaitWorkerStops()
		if got := sched.ScopedWorkerCounter(t, ops, "schedd_scale_up_decisions_total", map[string]string{"app": app.ID, "outcome": "reject_at_cap"}); got != float64(turn) {
			t.Fatalf("blocked demand counter = %v, want %d", got, turn)
		}
		if got := sched.ScopedWorkerCounter(t, ops, "schedd_scale_up_decisions_total", map[string]string{"app": app.ID, "outcome": "admit"}); got != 1 {
			t.Fatalf("actual admission counter = %v, want one", got)
		}
	}
	if (*vmm.ColdBoots()) != 1 {
		t.Fatalf("account-full environment booted another VM: %d", (*vmm.ColdBoots()))
	}
	recovery, err := store.WorkerRecoveryByApp(ctx, app.ID)
	if err != nil || len(recovery.Pools) != 2 {
		t.Fatalf("missing scope evidence: %+v %v", recovery, err)
	}
	if len(recovery.Pools[0].Blockers) != 0 {
		t.Fatalf("healthy default pool acquired blockers: %+v", recovery.Pools)
	}
	b := sched.RequireWorkerBlocker(t, recovery.Pools[1], "account_worker_limit")
	if b.Used == nil || *b.Used != 1 || b.Limit == nil || *b.Limit != 1 {
		t.Fatalf("account usage lost: %+v", b)
	}
}
