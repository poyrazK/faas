package sched_test

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// cronPlanStore is a gate store whose eligible set is precomputed per billing
// subject, so the gate's admit decision is tested independently of the
// ranking query (which pkg/state tests over MemStore and PgStore). Like the
// query, it ranks nothing under a zero cap. asked, when set, records the
// per-account cap each subject was ranked under.
type cronPlanStore struct {
	plans    map[string]api.Plan
	statuses map[string]state.AccountStatus // unset means active
	orgs     map[string]state.Org
	eligible map[string][]string
	asked    map[string]int
}

func (s cronPlanStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	plan, ok := s.plans[id]
	if !ok {
		return state.Account{}, errors.New("account unavailable")
	}
	status, ok := s.statuses[id]
	if !ok {
		status = state.AccountActive
	}
	return state.Account{ID: id, Plan: plan, Status: status}, nil
}

func (s cronPlanStore) OrgByID(_ context.Context, id string) (state.Org, error) {
	org, ok := s.orgs[id]
	if !ok {
		return state.Org{}, state.ErrNotFound
	}
	if org.Status == "" {
		org.Status = state.OrgStatusActive
	}
	return org, nil
}

func (s cronPlanStore) BillingSubjectEligibleCronIDs(_ context.Context, subjectID string, _, perAccount int) ([]string, error) {
	if s.asked != nil {
		s.asked[subjectID] = perAccount
	}
	if perAccount <= 0 {
		return nil, nil
	}
	return s.eligible[subjectID], nil
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

// A company app keeps its creator's account_id, but once the company adopted
// billing its crons fire under the company's plan and share the company's
// per-account allowance, whoever created the app (ADR-202610171904), as the
// cron was admitted when written. The gate read the creator's personal plan
// and ranked the creator's crons: a Free member's crons on a paid company app
// never fired.
//
// adr: 202610171904
func TestCronPlanGateRanksTheAppsBillingSubject(t *testing.T) {
	ctx := context.Background()
	asked := map[string]int{}
	admit := sched.NewCronPlanGate(cronPlanStore{
		plans: map[string]api.Plan{"free-member": api.PlanFree, "pro-member": api.PlanPro},
		orgs: map[string]state.Org{
			"adopted":   {ID: "adopted", Plan: api.PlanHobby, ResourceOwnershipEnabled: true},
			"unadopted": {ID: "unadopted", Plan: api.PlanScale},
		},
		eligible: map[string][]string{"adopted": {"co-1"}, "pro-member": {"own-1"}, "free-member": {"legacy-1"}},
		asked:    asked,
	})
	for _, step := range []struct {
		what string
		app  state.App
		cron string
		want bool
	}{
		{"a Free member's cron on the Hobby company's app", state.App{ID: "a1", AccountID: "free-member", OrgID: "adopted"}, "co-1", true},
		{"a Pro member's company cron past the company's allowance", state.App{ID: "a2", AccountID: "pro-member", OrgID: "adopted"}, "co-2", false},
		{"the Pro member's own cron", state.App{ID: "a3", AccountID: "pro-member"}, "own-1", true},
		// Until adoption a company app runs under its creator's plan.
		{"a Free creator's unadopted company cron", state.App{ID: "a4", AccountID: "free-member", OrgID: "unadopted"}, "legacy-1", false},
	} {
		if got := admit(ctx, step.app, state.Cron{ID: step.cron, AppID: step.app.ID}); got != step.want {
			t.Fatalf("%s: admitted = %v, want %v", step.what, got, step.want)
		}
	}
	want := map[string]int{
		"adopted":     api.MustLimitsFor(api.PlanHobby).CronLimitPerAccount,
		"pro-member":  api.MustLimitsFor(api.PlanPro).CronLimitPerAccount,
		"free-member": 0,
	}
	if !maps.Equal(asked, want) {
		t.Fatalf("ranked subjects (per-account cap) = %v, want %v", asked, want)
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

// A suspended account's crons do not fire, so the gate refuses them before
// the tick claims a fire it would only roll back.
func TestCronPlanGateRefusesSuspendedAccounts(t *testing.T) {
	ctx := context.Background()
	store := cronPlanStore{
		plans:    map[string]api.Plan{"active": api.PlanPro, "suspended": api.PlanPro},
		statuses: map[string]state.AccountStatus{"suspended": state.AccountSuspended},
		eligible: map[string][]string{"active": {"c1"}, "suspended": {"c2"}},
	}
	admit := sched.NewCronPlanGate(store)
	if !admit(ctx, state.App{ID: "a1", AccountID: "active"}, state.Cron{ID: "c1", AppID: "a1"}) {
		t.Fatal("an active account's eligible cron was refused")
	}
	if admit(ctx, state.App{ID: "a2", AccountID: "suspended"}, state.Cron{ID: "c2", AppID: "a2"}) {
		t.Fatal("a suspended account's cron was admitted")
	}
}
