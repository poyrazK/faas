// Package cronrun decides whether a manual cron run fits the owner's current
// plan. After a downgrade schedd keeps firing only the oldest crons the new
// plan allows (pkg/sched cronPlanGate); a manual run of one of the others
// would wake the app outside that allowance, so apid refuses it up front.
package cronrun

import (
	"context"
	"math"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store is what OverPlan reads: the app's owner, the owner's eligible crons
// and the app's crons.
type Store interface {
	state.AppOwnerReader
	state.CronAccountQuotaStore
	ListCronsForApp(ctx context.Context, appID string) ([]state.Cron, error)
}

// OverPlan returns a 403 plan_cron_quota problem when c is not among the crons
// its billing subject's plan lets fire, or nil. It ranks crons the way the
// scheduler's gate does, so a cron is refused here exactly when its schedule
// would not fire. A disabled or suspended cron is left to the scheduler, which
// records why it did not run. Read errors admit, as the scheduler's gate does:
// the cron passed its plan's limits when it was written.
func OverPlan(ctx context.Context, store Store, app state.App, c state.Cron) *api.Problem {
	if !c.Enabled || c.SuspendedReason != "" {
		return nil
	}
	r, refused := refuse(ctx, store, app, c)
	if !refused {
		return nil
	}
	return api.ErrCronRunOverPlan(r.plan, r.scope, r.limit, r.observed)
}

// refusal is the plan limit a cron is over.
type refusal struct {
	plan            api.Plan
	scope           string
	limit, observed int
}

// refuse reports the limit c is over, or false when c may run or a read
// failed.
func refuse(ctx context.Context, store Store, app state.App, c state.Cron) (refusal, bool) {
	owner, err := state.AppResourceAccount(ctx, store, app)
	if err != nil {
		return refusal{}, false
	}
	limits, ok := api.LimitsFor(owner.Plan)
	if !ok {
		return refusal{}, false
	}
	subject := owner.ResourceOwnerID()
	eligible, err := store.BillingSubjectEligibleCronIDs(ctx, subject, limits.CronLimitPerApp, limits.CronLimitPerAccount)
	if err != nil || contains(eligible, c.ID) {
		return refusal{}, false
	}
	if rank, count, err := appRank(ctx, store, c); err == nil && rank > limits.CronLimitPerApp {
		return refusal{owner.Plan, string(state.CronQuotaScopeApp), limits.CronLimitPerApp, count}, true
	}
	withinApps, err := store.BillingSubjectEligibleCronIDs(ctx, subject, limits.CronLimitPerApp, math.MaxInt32)
	if err != nil {
		return refusal{}, false
	}
	return refusal{owner.Plan, string(state.CronQuotaScopeAccount), limits.CronLimitPerAccount, len(withinApps)}, true
}

// appRank returns c's 1-based age rank among its app's enabled, unsuspended
// crons and how many there are.
func appRank(ctx context.Context, store Store, c state.Cron) (int, int, error) {
	crons, err := store.ListCronsForApp(ctx, c.AppID)
	if err != nil {
		return 0, 0, err
	}
	live := crons[:0]
	for _, x := range crons {
		if x.Enabled && x.SuspendedReason == "" {
			live = append(live, x)
		}
	}
	sort.Slice(live, func(i, j int) bool {
		if !live[i].CreatedAt.Equal(live[j].CreatedAt) {
			return live[i].CreatedAt.Before(live[j].CreatedAt)
		}
		return live[i].ID < live[j].ID
	})
	for i, x := range live {
		if x.ID == c.ID {
			return i + 1, len(live), nil
		}
	}
	return 0, len(live), nil
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
