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
type cronPlanGate struct {
	accounts interface {
		AccountByID(context.Context, string) (state.Account, error)
	}
	plans   map[string]api.Plan
	perAcct map[string]int
}

func newCronPlanGate(accounts interface {
	AccountByID(context.Context, string) (state.Account, error)
}) *cronPlanGate {
	return &cronPlanGate{accounts: accounts, plans: map[string]api.Plan{}, perAcct: map[string]int{}}
}

// admit reports whether a due cron of app, the appRank-th oldest of the app's
// enabled crons, may fire this tick, and counts it against the account when
// it may. The per-app rank is exact; the per-account count covers the crons
// that fire this tick. A failed account read admits: the cron passed its
// plan's limits when it was written.
func (g *cronPlanGate) admit(ctx context.Context, app state.App, appRank int) bool {
	plan, seen := g.plans[app.AccountID]
	if !seen {
		if acct, err := g.accounts.AccountByID(ctx, app.AccountID); err == nil {
			plan = acct.Plan
		}
		g.plans[app.AccountID] = plan
	}
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return true
	}
	if appRank >= limits.CronLimitPerApp || g.perAcct[app.AccountID] >= limits.CronLimitPerAccount {
		return false
	}
	g.perAcct[app.AccountID]++
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
