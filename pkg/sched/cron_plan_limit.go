package sched

import (
	"cmp"
	"context"
	"slices"

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
	perApp  map[string]int
	perAcct map[string]int
}

func newCronPlanGate(accounts interface {
	AccountByID(context.Context, string) (state.Account, error)
}) *cronPlanGate {
	return &cronPlanGate{accounts: accounts, plans: map[string]api.Plan{}, perApp: map[string]int{}, perAcct: map[string]int{}}
}

// admit reports whether a cron of app may fire this tick, and counts it when
// it may. A failed account read admits: the cron passed its plan's limits
// when it was written.
func (g *cronPlanGate) admit(ctx context.Context, app state.App) bool {
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
	if g.perApp[app.ID] >= limits.CronLimitPerApp || g.perAcct[app.AccountID] >= limits.CronLimitPerAccount {
		return false
	}
	g.perApp[app.ID]++
	g.perAcct[app.AccountID]++
	return true
}

// oldestCronsFirst orders crons so the plan gate keeps the ones created
// first.
func oldestCronsFirst(crons []state.Cron) {
	slices.SortStableFunc(crons, func(a, b state.Cron) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
}
