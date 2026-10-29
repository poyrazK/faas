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

// cronPlanStore is a gate store whose eligible set is precomputed per account,
// so the gate's admit decision is tested independently of the ranking query
// (which pkg/state tests over MemStore and PgStore).
type cronPlanStore struct {
	plans    map[string]api.Plan
	orgs     map[string]state.Org
	eligible map[string][]string
}

func (s cronPlanStore) OrgByID(_ context.Context, id string) (state.Org, error) {
	org, ok := s.orgs[id]
	if !ok {
		return state.Org{}, state.ErrNotFound
	}
	return org, nil
}

func (s cronPlanStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	plan, ok := s.plans[id]
	if !ok {
		return state.Account{}, errors.New("account unavailable")
	}
	return state.Account{ID: id, Plan: plan}, nil
}

func (s cronPlanStore) AccountEligibleCronIDs(_ context.Context, accountID string, _, _ int) ([]string, error) {
	return s.eligible[accountID], nil
}

// A downgrade left every cron firing against the new plan's quota. The gate
// fires only the crons the store ranks as eligible, denies the rest, denies a
// Free account's crons entirely, and admits a cron whose account cannot be read
// (it passed its limits when written).
func TestCronPlanGateAdmitsOnlyEligibleCrons(t *testing.T) {
	ctx := context.Background()
	store := cronPlanStore{
		plans:    map[string]api.Plan{"free": api.PlanFree, "hobby": api.PlanHobby},
		eligible: map[string][]string{"hobby": {"c1", "c2"}},
	}
	admit := sched.NewCronPlanGate(store)
	app := state.App{ID: "a1", AccountID: "hobby"}

	if !admit(ctx, app, state.Cron{ID: "c1", AppID: "a1"}) {
		t.Fatal("eligible cron c1 denied")
	}
	if !admit(ctx, app, state.Cron{ID: "c2", AppID: "a1"}) {
		t.Fatal("eligible cron c2 denied")
	}
	if admit(ctx, app, state.Cron{ID: "c9", AppID: "a1"}) {
		t.Fatal("a cron past the account limit fired")
	}
	if admit(ctx, state.App{ID: "f1", AccountID: "free"}, state.Cron{ID: "x", AppID: "f1"}) {
		t.Fatal("a Free account's cron fired; Free allows none")
	}
	if !admit(ctx, state.App{ID: "u1", AccountID: "unreadable"}, state.Cron{ID: "z", AppID: "u1"}) {
		t.Fatal("a cron whose account could not be read was held; it passed its limits when written")
	}
}

// A company app's crons follow the organization's plan and cap, not the
// personal plan of the member who created the app: a paid organization's
// cron created by a member on Free was skipped on every tick (bug hunt
// 2026-10-28 B37).
func TestCronPlanGateUsesTheOrganizationsPlan(t *testing.T) {
	ctx := context.Background()
	store := cronPlanStore{
		plans:    map[string]api.Plan{"member-free": api.PlanFree},
		orgs:     map[string]state.Org{"acme": {ID: "acme", Plan: api.PlanPro, ResourceOwnershipEnabled: true, Status: "active"}},
		eligible: map[string][]string{"acme": {"c1"}},
	}
	admit := sched.NewCronPlanGate(store)
	app := state.App{ID: "a1", AccountID: "member-free", OrgID: "acme"}
	if !admit(ctx, app, state.Cron{ID: "c1", AppID: "a1"}) {
		t.Fatal("a Pro organization's cron was judged by its creator's Free plan")
	}
	if admit(ctx, app, state.Cron{ID: "c2", AppID: "a1"}) {
		t.Fatal("a cron past the organization's cap fired")
	}
	gone := state.App{ID: "a2", AccountID: "erased-member", OrgID: "acme"}
	if !admit(ctx, gone, state.Cron{ID: "c1", AppID: "a2"}) {
		t.Fatal("a company cron stopped when its creator's account was erased")
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
