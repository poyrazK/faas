package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSchedFloorPlanResolver_InactiveAccountHasNoFloor — the floor trigger
// kept waking apps of suspended accounts; the engine refused every wake,
// so each such app logged "floor: admit error" and counted a reconcile
// error every backoff period, forever.
func TestSchedFloorPlanResolver_InactiveAccountHasNoFloor(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "floor@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	r := schedFloorPlanResolver{store: store}
	for _, tc := range []struct {
		status state.AccountStatus
		want   api.Plan
	}{
		{state.AccountActive, api.PlanPro},
		{state.AccountPastDue, api.PlanPro}, // apps keep serving during the grace period
		{state.AccountSuspended, api.PlanFree},
		{state.AccountDeletedPending, api.PlanFree},
	} {
		if err := store.UpdateAccountStatus(ctx, acct.ID, tc.status); err != nil {
			t.Fatal(err)
		}
		plan, ok := r.ResolvePlan(ctx, acct.ID)
		if !ok || plan != tc.want {
			t.Errorf("%s: ResolvePlan = %s, %v; want %s, true", tc.status, plan, ok, tc.want)
		}
		if tc.status != state.AccountActive && tc.status != state.AccountPastDue && plan.MinInstancesAllowed() {
			t.Errorf("%s: resolved plan still grants a floor", tc.status)
		}
	}
}
