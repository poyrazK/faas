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
