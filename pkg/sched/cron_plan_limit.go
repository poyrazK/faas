package sched

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// cronPlanGate admits, within one tick, only the crons the owning account's
// current plan allows. Cron counts are capped when crons are written, but a
// downgrade or an ended subscription left every cron firing, and waking its
// app, against the new plan's quota (Free allows none). The oldest crons keep
// firing; the rest wait until the plan allows them again.
//
// The owning account is the app's billing subject (ADR-202610171904), whose
// plan admitted the cron when it was written: a company once it adopted
// billing, with the company's plan and one allowance across its members, and
// otherwise the app's creator. A company app keeps its creator's account_id,
// so the creator's personal plan must not gate it.
type cronPlanGate struct {
	store   state.AppOwnerReader
	owners  map[string]state.Account
	perAcct map[string]int
}

func newCronPlanGate(store state.AppOwnerReader) *cronPlanGate {
	return &cronPlanGate{store: store, owners: map[string]state.Account{}, perAcct: map[string]int{}}
}

// admit reports whether a due cron of app, the appRank-th oldest of the app's
// enabled crons, may fire this tick, and counts it against the app's billing
// subject when it may. The per-app rank is exact; the per-account count covers
// the crons that fire this tick. A failed owner read admits: the cron passed
// its plan's limits when it was written.
func (g *cronPlanGate) admit(ctx context.Context, app state.App, appRank int) bool {
	key := app.OrgID + "/" + app.AccountID
	owner, seen := g.owners[key]
	if !seen {
		if acct, err := state.AppResourceAccount(ctx, g.store, app); err == nil {
			owner = acct
		}
		g.owners[key] = owner
	}
	limits, ok := api.LimitsFor(owner.Plan)
	if !ok {
		return true
	}
	subject := owner.ResourceOwnerID()
	if appRank >= limits.CronLimitPerApp || g.perAcct[subject] >= limits.CronLimitPerAccount {
		return false
	}
	g.perAcct[subject]++
	return true
}

// oldestCronsFirst orders crons so the plan gate keeps the ones created
// first, and returns each cron's rank among its app's crons.
func oldestCronsFirst(crons []state.Cron) map[string]int {
	slices.SortStableFunc(crons, func(a, b state.Cron) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	seen := map[string]int{}
	rank := make(map[string]int, len(crons))
	for _, c := range crons {
		rank[c.ID] = seen[c.AppID]
		seen[c.AppID]++
	}
	return rank
}

// cronDue reports whether c has an occurrence at or before now, with no
// store read, so a cron that is not due costs nothing (a daily cron used to
// cost an app read every minute). A disabled or unparseable cron reports due
// so dispatchOneCron records its decision.
func cronDue(c state.Cron, now time.Time) bool {
	if !c.Enabled {
		return true
	}
	sched, err := ParseScheduleWithTimezone(c.Schedule, c.Timezone)
	if err != nil {
		return true
	}
	return !sched.NextFireAt(c.ScheduleCursor()).After(now)
}
