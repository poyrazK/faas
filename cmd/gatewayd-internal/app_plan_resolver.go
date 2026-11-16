package main

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// appPlanStore is what the resolver reads; PgStore satisfies it.
type appPlanStore interface {
	state.AppOwnerReader
	AppByID(ctx context.Context, id string) (state.App, error)
}

// appPlanCacheTTL bounds how stale a service call's metric plan can be. The
// plan only caps an app's deployment series, so a minute-scale lag after a
// plan change is harmless and keeps the service path off Postgres.
const appPlanCacheTTL = 5 * time.Minute

type appPlanEntry struct {
	plan    api.Plan
	ok      bool
	expires time.Time
}

// newAppPlanResolver resolves a service call target's plan for request
// metrics (gateway.Handler.WithAppPlanResolver), cached per app.
func newAppPlanResolver(store appPlanStore) gateway.AppPlanResolver {
	var (
		mu    sync.Mutex
		cache = map[string]appPlanEntry{}
	)
	return func(ctx context.Context, appID string) (api.Plan, bool) {
		now := time.Now()
		mu.Lock()
		if e, hit := cache[appID]; hit && now.Before(e.expires) {
			mu.Unlock()
			return e.plan, e.ok
		}
		mu.Unlock()
		entry := appPlanEntry{expires: now.Add(appPlanCacheTTL)}
		if app, err := store.AppByID(ctx, appID); err == nil {
			if acct, err := state.AppResourceAccount(ctx, store, app); err == nil {
				entry.plan, entry.ok = acct.Plan, true
			}
		}
		mu.Lock()
		if len(cache) > 10_000 {
			cache = map[string]appPlanEntry{}
		}
		cache[appID] = entry
		mu.Unlock()
		return entry.plan, entry.ok
	}
}
