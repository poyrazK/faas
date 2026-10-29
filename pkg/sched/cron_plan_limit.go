package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// cronPlanGate admits only the crons the owning account's current plan allows.
// Cron counts are capped when crons are written, but a downgrade or an ended
// subscription left every cron firing, and waking its app, against the new
// plan's quota (Free allows none). The oldest crons keep firing; the rest wait
// until the plan allows them again.
//
// The eligible set is resolved once per account per tick from the store, which
// ranks the account's crons globally by age. Every schedd node therefore agrees
// on the same set, so the per-account cap holds across the fleet rather than
// once per node (a node-local per-tick counter let an account whose apps span N
// nodes keep up to N x CronLimitPerAccount crons firing after a downgrade).
type cronPlanGate struct {
	store    cronPlanGateStore
	eligible map[string]map[string]struct{} // accountID -> eligible cron ids; nil value = admit all
}

type cronPlanGateStore interface {
	AccountByID(context.Context, string) (state.Account, error)
}

func newCronPlanGate(store cronPlanGateStore) *cronPlanGate {
	return &cronPlanGate{store: store, eligible: map[string]map[string]struct{}{}}
}

// admit reports whether the given due cron of app may fire. A nil eligible set
// (the account or its plan could not be read, or the store cannot rank crons)
// admits: the cron passed its plan's limits when it was written.
func (g *cronPlanGate) admit(ctx context.Context, app state.App, cron state.Cron) bool {
	set, seen := g.eligible[app.AccountID]
	if !seen {
		set = g.loadEligible(ctx, app.AccountID)
		g.eligible[app.AccountID] = set
	}
	if set == nil {
		return true
	}
	_, ok := set[cron.ID]
	return ok
}

// loadEligible resolves the account's eligible cron ids, or nil to admit all.
// An empty (non-nil) set correctly denies every cron — a Free account's 0 cap.
func (g *cronPlanGate) loadEligible(ctx context.Context, accountID string) map[string]struct{} {
	quota, ok := g.store.(state.CronAccountQuotaStore)
	if !ok {
		return nil
	}
	acct, err := g.store.AccountByID(ctx, accountID)
	if err != nil {
		return nil
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return nil
	}
	ids, err := quota.AccountEligibleCronIDs(ctx, accountID, limits.CronLimitPerApp, limits.CronLimitPerAccount)
	if err != nil {
		return nil
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
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
