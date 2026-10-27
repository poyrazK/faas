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
	if admit(ctx, state.App{ID: "f1", AccountID: "free"}) {
		t.Fatal("a Free account's cron fired; Free allows none")
	}

	hobby := api.MustLimitsFor(api.PlanHobby)
	admitted := 0
	for range hobby.CronLimitPerApp + 3 {
		if admit(ctx, state.App{ID: "h1", AccountID: "hobby"}) {
			admitted++
		}
	}
	if admitted != hobby.CronLimitPerApp {
		t.Fatalf("Hobby app fired %d crons, want %d", admitted, hobby.CronLimitPerApp)
	}
	for i := range hobby.CronLimitPerAccount {
		admit(ctx, state.App{ID: "h-other-" + string(rune('a'+i)), AccountID: "hobby"})
	}
	if admit(ctx, state.App{ID: "h-last", AccountID: "hobby"}) {
		t.Fatal("a cron past the account limit fired")
	}

	if !admit(ctx, state.App{ID: "u1", AccountID: "unreadable"}) {
		t.Fatal("a cron whose account could not be read was held; it passed its limits when written")
	}
}

func TestOldestCronsFirst(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	crons := []state.Cron{
		{ID: "c", CreatedAt: now},
		{ID: "a", CreatedAt: now.Add(-time.Hour)},
		{ID: "b", CreatedAt: now},
	}
	sched.OldestCronsFirst(crons)
	if crons[0].ID != "a" || crons[1].ID != "b" || crons[2].ID != "c" {
		t.Fatalf("order = %s,%s,%s; want a,b,c", crons[0].ID, crons[1].ID, crons[2].ID)
	}
}
