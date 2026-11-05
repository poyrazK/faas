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
// The owning account is the app's billing subject (ADR-202610171904), whose
// plan admitted the cron when it was written: a company once it adopted
// billing, with the company's plan and one allowance across its members, and
// otherwise the app's creator. A company app keeps its creator's account_id,
// so the creator's personal plan and crons must not gate it.
//
// The eligible set is resolved once per subject per tick from the store, which
// ranks the subject's crons globally by age. Every schedd node therefore agrees
// on the same set, so the per-account cap holds across the fleet rather than
// once per node (a node-local per-tick counter let an account whose apps span N
// nodes keep up to N x CronLimitPerAccount crons firing after a downgrade).
type cronPlanGate struct {
	store    state.AppOwnerReader
	owners   map[string]*state.Account      // org_id/account_id -> resource owner; nil = unreadable
	eligible map[string]map[string]struct{} // subject -> eligible cron ids; nil value = admit all
}

func newCronPlanGate(store state.AppOwnerReader) *cronPlanGate {
	return &cronPlanGate{store: store, owners: map[string]*state.Account{}, eligible: map[string]map[string]struct{}{}}
}

// admit reports whether the given due cron of app may fire. A nil eligible set
// (the app's owner or its plan could not be read, or the store cannot rank
// crons) admits: the cron passed its plan's limits when it was written.
func (g *cronPlanGate) admit(ctx context.Context, app state.App, cron state.Cron) bool {
	key := app.OrgID + "/" + app.AccountID
	owner, seen := g.owners[key]
	if !seen {
		if acct, err := state.AppResourceAccount(ctx, g.store, app); err == nil {
			owner = &acct
		}
		g.owners[key] = owner
	}
	if owner == nil {
		return true
	}
	// A suspended owner's crons do not fire (dispatchCronLocked). Refusing
	// them here also skips the fire claim and its rollback: two writes per
	// cron per minute for as long as the account stays suspended.
	if !owner.Active() {
		return false
	}
	subject := owner.ResourceOwnerID()
	set, seen := g.eligible[subject]
	if !seen {
		set = g.loadEligible(ctx, subject, owner.Plan)
		g.eligible[subject] = set
	}
	if set == nil {
		return true
	}
	_, ok := set[cron.ID]
	return ok
}

// loadEligible resolves the subject's eligible cron ids, or nil to admit all.
// An empty (non-nil) set correctly denies every cron — a Free account's 0 cap.
func (g *cronPlanGate) loadEligible(ctx context.Context, subject string, plan api.Plan) map[string]struct{} {
	quota, ok := g.store.(state.CronAccountQuotaStore)
	if !ok {
		return nil
	}
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return nil
	}
	ids, err := quota.BillingSubjectEligibleCronIDs(ctx, subject, limits.CronLimitPerApp, limits.CronLimitPerAccount)
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
