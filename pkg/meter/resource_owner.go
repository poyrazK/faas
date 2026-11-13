package meter

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func meterAppSubject(app state.App, plans map[string]api.Plan) string {
	if _, shared := plans[app.OrgID]; shared {
		return app.OrgID
	}
	return app.AccountID
}

// planFloorCap is the most warm instances schedd's floor trigger holds for
// app under plan (pkg/sched/floor): none when the plan has no floors, else
// no more than the plan's MaxMinInstances and the app's effective max
// concurrency.
func planFloorCap(app state.App, plan api.Plan) int {
	limits, ok := api.LimitsFor(plan)
	if !ok || !plan.MinInstancesAllowed() {
		return 0
	}
	maxConcurrency := limits.MaxConcurrency
	if app.MaxConcurrency > 0 && app.MaxConcurrency < maxConcurrency {
		maxConcurrency = app.MaxConcurrency
	}
	return min(limits.MaxMinInstances, maxConcurrency)
}

// floorHolders answers, once per account and organization per tick, whether
// schedd holds an app's capacity floor. It reads the same resource account
// schedd's reaper and lifecycle reconcile use, so a company app follows its
// organization's suspension or deletion, and the same spend cap check schedd's
// admission uses: at the cap schedd parks the subject's floors, so the floor
// is not billed (ADR-20261103131742 amendment 2). A failed read keeps billing
// the floor, as schedd keeps holding it when the same read fails.
type floorHolders struct {
	store state.Store
	at    time.Time
	caps  map[string]int64 // loaded on first use; nil until then
	seen  map[[2]string]bool
}

func newFloorHolders(store state.Store, at time.Time) *floorHolders {
	return &floorHolders{store: store, at: at, seen: map[[2]string]bool{}}
}

func (f *floorHolders) forApp(ctx context.Context, app state.App) bool {
	key := [2]string{app.AccountID, app.OrgID}
	if held, ok := f.seen[key]; ok {
		return held
	}
	held := true
	if account, err := state.AppResourceAccount(ctx, f.store, app); err == nil {
		held = account.Active() && !f.capReached(ctx, account.ID)
	}
	f.seen[key] = held
	return held
}

// capReached mirrors schedd's spend cap admission (pkg/sched/overage.go):
// the month's compute overage at or past the subject's cap, or meterd's hold
// for that cap and month. A failed read counts as not reached.
func (f *floorHolders) capReached(ctx context.Context, subject string) bool {
	if f.caps == nil {
		caps, err := f.store.LoadAllOverageCapCents(ctx)
		if err != nil {
			return false
		}
		f.caps = caps
	}
	capCents, capped := f.caps[subject]
	if !capped {
		return false
	}
	if observed, err := f.store.CurrentMonthOverageCents(ctx, subject); err == nil && observed >= capCents {
		return true
	}
	holds, ok := f.store.(state.SpendCapHoldStore)
	if !ok {
		return false
	}
	hold, found, err := holds.GetSpendCapHold(ctx, subject)
	return err == nil && found && hold.Holds(capCents, f.at)
}
