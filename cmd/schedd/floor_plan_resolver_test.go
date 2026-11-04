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

// A company app's floor follows its organization's plan and status, the
// subject meterd bills the floor to: a paid organization of a Free member
// holds a floor, and a suspended organization holds none.
// adr: 20261101113742
func TestSchedFloorPlanResolver_CompanyAppFollowsOrganization(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	member, err := store.CreateAccount(ctx, "member@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	// MemStore turns resource ownership on for an organization created Free.
	org, err := store.CreateOrg(ctx, state.Org{Slug: "team", Name: "Team", Plan: api.PlanFree, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOrgPlan(ctx, org.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	app := state.App{AccountID: member.ID, OrgID: org.ID}
	r := schedFloorPlanResolver{store: store}
	if plan, ok := r.ResolveAppPlan(ctx, app); !ok || plan != api.PlanPro {
		t.Fatalf("ResolveAppPlan = %s, %v; want the organization's plan pro", plan, ok)
	}
	if plan, ok := r.ResolveAppPlan(ctx, state.App{AccountID: member.ID}); !ok || plan != api.PlanFree {
		t.Fatalf("personal app ResolveAppPlan = %s, %v; want the member's plan free", plan, ok)
	}
	if err := store.UpdateOrgStatus(ctx, org.ID, state.OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	if plan, ok := r.ResolveAppPlan(ctx, app); !ok || plan.MinInstancesAllowed() {
		t.Fatalf("suspended organization ResolveAppPlan = %s, %v; want a plan without floors", plan, ok)
	}
}
