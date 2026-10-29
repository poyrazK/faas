package meter

import (
	"context"

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
// organization's suspension or deletion. A failed read keeps billing the
// floor, as schedd keeps holding it when the same read fails.
type floorHolders struct {
	store state.AppOwnerReader
	seen  map[[2]string]bool
}

func newFloorHolders(store state.AppOwnerReader) *floorHolders {
	return &floorHolders{store: store, seen: map[[2]string]bool{}}
}

func (f *floorHolders) forApp(ctx context.Context, app state.App) bool {
	key := [2]string{app.AccountID, app.OrgID}
	if held, ok := f.seen[key]; ok {
		return held
	}
	held := true
	if account, err := state.AppResourceAccount(ctx, f.store, app); err == nil {
		held = account.Active()
	}
	f.seen[key] = held
	return held
}
