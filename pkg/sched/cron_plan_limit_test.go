package sched_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type cronPlanAccounts map[string]api.Plan

func (a cronPlanAccounts) AccountByID(_ context.Context, id string) (state.Account, error) {
	plan, ok := a[id]
	if !ok {
		return state.Account{}, errors.New("account unavailable")
	}
	return state.Account{ID: id, Plan: plan}, nil
}

func (a cronPlanAccounts) OrgByID(context.Context, string) (state.Org, error) {
	return state.Org{}, state.ErrNotFound
}

type cronPlanOwners struct {
	cronPlanAccounts
	orgs map[string]state.Org
}

func (o cronPlanOwners) OrgByID(_ context.Context, id string) (state.Org, error) {
	org, ok := o.orgs[id]
	if !ok {
		return state.Org{}, state.ErrNotFound
	}
	return org, nil
}

// A downgrade left every cron firing against the new plan's quota. The gate
// keeps the oldest crons within the plan's per-app and per-account limits.
func TestCronPlanGateKeepsCronsWithinThePlan(t *testing.T) {
	ctx := context.Background()
	admit := sched.NewCronPlanGate(cronPlanAccounts{"free": api.PlanFree, "hobby": api.PlanHobby})
	if admit(ctx, state.App{ID: "f1", AccountID: "free"}, 0) {
		t.Fatal("a Free account's cron fired; Free allows none")
	}

	hobby := api.MustLimitsFor(api.PlanHobby)
	admitted := 0
	for rank := range hobby.CronLimitPerApp + 3 {
		if admit(ctx, state.App{ID: "h1", AccountID: "hobby"}, rank) {
			admitted++
		}
	}
	if admitted != hobby.CronLimitPerApp {
		t.Fatalf("Hobby app fired %d crons, want %d", admitted, hobby.CronLimitPerApp)
	}
	for i := range hobby.CronLimitPerAccount {
		admit(ctx, state.App{ID: "h-other-" + string(rune('a'+i)), AccountID: "hobby"}, 0)
	}
	if admit(ctx, state.App{ID: "h-last", AccountID: "hobby"}, 0) {
		t.Fatal("a cron past the account limit fired")
	}

	if !admit(ctx, state.App{ID: "u1", AccountID: "unreadable"}, 0) {
		t.Fatal("a cron whose account could not be read was held; it passed its limits when written")
	}
}

// A company app keeps its creator's account_id, but once the company adopted
// billing its crons fire under the company's plan and share the company's
// per-account allowance, whoever created the app (ADR-202610171904), as the
// cron was admitted when written. The gate read the creator's personal plan:
// a Free member's crons on a Pro company app never fired.
//
// adr: 202610171904
func TestCronPlanGateUsesTheAppsBillingSubject(t *testing.T) {
	ctx := context.Background()
	admit := sched.NewCronPlanGate(cronPlanOwners{
		cronPlanAccounts: cronPlanAccounts{"free-member": api.PlanFree, "hobby-member": api.PlanHobby, "pro-member": api.PlanPro},
		orgs: map[string]state.Org{
			"adopted":   {ID: "adopted", Plan: api.PlanHobby, ResourceOwnershipEnabled: true},
			"unadopted": {ID: "unadopted", Plan: api.PlanScale},
		},
	})
	hobby := api.MustLimitsFor(api.PlanHobby)
	app := func(id, accountID, orgID string) state.App { return state.App{ID: id, AccountID: accountID, OrgID: orgID} }

	if !admit(ctx, app("co-1", "free-member", "adopted"), 0) {
		t.Fatal("a Free member's cron on a Hobby company app was held; the company's plan allows it")
	}
	// The company's crons share one allowance across its members.
	for i := 1; i < hobby.CronLimitPerAccount; i++ {
		if !admit(ctx, app("co-"+string(rune('a'+i)), "hobby-member", "adopted"), 0) {
			t.Fatalf("company cron %d of %d was held", i+1, hobby.CronLimitPerAccount)
		}
	}
	for _, member := range []string{"pro-member", "hobby-member"} {
		if admit(ctx, app("co-over-"+member, member, "adopted"), 0) {
			t.Fatalf("%s's company cron fired past the Hobby company's allowance", member)
		}
	}
	// The company's crons never spend a member's personal allowance.
	if !admit(ctx, app("own", "hobby-member", ""), 0) {
		t.Fatal("a member's personal cron was held by the company's crons")
	}
	// Until adoption a company app runs under its creator's plan and shares
	// the creator's allowance.
	if admit(ctx, app("legacy", "free-member", "unadopted"), 0) {
		t.Fatal("a Free creator's unadopted company cron fired under the company's plan")
	}
}

func TestOldestCronsFirst(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	crons := []state.Cron{
		{ID: "c", AppID: "app-1", CreatedAt: now},
		{ID: "a", AppID: "app-1", CreatedAt: now.Add(-time.Hour)},
		{ID: "b", AppID: "app-2", CreatedAt: now},
	}
	rank := sched.OldestCronsFirst(crons)
	if crons[0].ID != "a" || crons[1].ID != "b" || crons[2].ID != "c" {
		t.Fatalf("order = %s,%s,%s; want a,b,c", crons[0].ID, crons[1].ID, crons[2].ID)
	}
	if rank["a"] != 0 || rank["c"] != 1 || rank["b"] != 0 {
		t.Fatalf("per-app ranks = %v", rank)
	}
}

// A daily cron cost an app read every minute before the code checked
// whether it was due (bug hunt 2026-10-27 O6). Due-ness is now decided
// first, with no read.
func TestCronDue(t *testing.T) {
	created := time.Date(2026, 10, 27, 9, 30, 0, 0, time.UTC)
	daily := state.Cron{ID: "d", Enabled: true, Schedule: "0 10 * * *", CreatedAt: created}
	if sched.CronDue(daily, created.Add(10*time.Minute)) {
		t.Fatal("daily 10:00 cron due at 09:40")
	}
	if !sched.CronDue(daily, created.Add(30*time.Minute)) {
		t.Fatal("daily 10:00 cron not due at 10:00")
	}
}
