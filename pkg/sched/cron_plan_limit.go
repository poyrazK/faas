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
//
// The plan and the cap are the app's billing subject's: for a company app with
// resource ownership that is the organization, not the member who created the
// app. Reading the creator skipped every cron of a paid organization's app
// whose creator was on a personal Free plan, and split one organization's
// crons across its members' personal caps (bug hunt 2026-10-28 B37).
type cronPlanGate struct {
	store    cronPlanGateStore
	subjects map[string]cronPlanSubject       // app ID -> its billing subject this tick
	eligible map[string]map[string]struct{} // subject ID -> eligible cron ids; nil value = admit all
}

type cronPlanSubject struct {
	id   string
	plan api.Plan
	ok   bool
}

type cronPlanGateStore interface {
	state.AppOwnerReader
}

func newCronPlanGate(store cronPlanGateStore) *cronPlanGate {
	return &cronPlanGate{store: store, subjects: map[string]cronPlanSubject{}, eligible: map[string]map[string]struct{}{}}
}

// admit reports whether the given due cron of app may fire. A nil eligible set
// (the subject or its plan could not be read, or the store cannot rank crons)
// admits: the cron passed its plan's limits when it was written.
func (g *cronPlanGate) admit(ctx context.Context, app state.App, cron state.Cron) bool {
	subject, seen := g.subjects[app.ID]
	if !seen {
		if acct, err := state.AppResourceAccount(ctx, g.store, app); err == nil {
			subject = cronPlanSubject{id: acct.ResourceOwnerID(), plan: acct.Plan, ok: true}
		}
		g.subjects[app.ID] = subject
	}
	if !subject.ok {
		return true
	}
	set, seen := g.eligible[subject.id]
	if !seen {
		set = g.loadEligible(ctx, subject)
		g.eligible[subject.id] = set
	}
	if set == nil {
		return true
	}
	_, ok := set[cron.ID]
	return ok
}

// loadEligible resolves the subject's eligible cron ids, or nil to admit all.
// An empty (non-nil) set correctly denies every cron — a Free account's 0 cap.
func (g *cronPlanGate) loadEligible(ctx context.Context, subject cronPlanSubject) map[string]struct{} {
	quota, ok := g.store.(state.CronAccountQuotaStore)
	if !ok {
		return nil
	}
	limits, ok := api.LimitsFor(subject.plan)
	if !ok {
		return nil
	}
	ids, err := quota.AccountEligibleCronIDs(ctx, subject.id, limits.CronLimitPerApp, limits.CronLimitPerAccount)
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
