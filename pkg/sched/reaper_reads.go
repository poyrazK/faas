package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// The reaper ticks every 10 s and visits every owned app. Its per-app reads
// were a steady share of the production control plane's Postgres CPU: two
// full instance histories (thousands of parked rows per app) and two owner
// account reads per app per tick. The helpers below read live rows only and
// each owner once per tick.

// reaperLiveInstances reads the live instances of every reaped app in one
// query, keyed by app. A nil map means the store cannot or the read failed;
// the caller then falls back to one ListInstancesForApp per app.
func (l *Loop) reaperLiveInstances(ctx context.Context, store state.Store, apps []state.App) map[string][]state.Instance {
	reader, ok := store.(state.LiveInstancesForAppsReader)
	if !ok || len(apps) == 0 {
		return nil
	}
	ids := make([]string, 0, len(apps))
	for _, a := range apps {
		ids = append(ids, a.ID)
	}
	rows, err := reader.ListLiveInstancesForApps(ctx, ids)
	if err != nil {
		l.log.Warn("reaper: list live instances", "apps", len(ids), "err", err)
		return nil
	}
	out := make(map[string][]state.Instance, len(apps))
	for _, ins := range rows {
		out[ins.AppID] = append(out[ins.AppID], ins)
	}
	return out
}

// reaperAccountKey is what state.AppResourceAccount resolves an app's owner
// from.
type reaperAccountKey struct{ accountID, orgID string }

type reaperAccount struct {
	account state.Account
	err     error
}

// reaperAccounts memoizes state.AppResourceAccount for one reaper tick. Apps
// of one account or organization share an owner, so a tick reads it once.
type reaperAccounts struct {
	store state.AppOwnerReader
	seen  map[reaperAccountKey]reaperAccount
}

func newReaperAccounts(store state.AppOwnerReader) *reaperAccounts {
	return &reaperAccounts{store: store, seen: map[reaperAccountKey]reaperAccount{}}
}

func (r *reaperAccounts) forApp(ctx context.Context, app state.App) (state.Account, error) {
	key := reaperAccountKey{accountID: app.AccountID, orgID: app.OrgID}
	if got, ok := r.seen[key]; ok {
		return got.account, got.err
	}
	account, err := state.AppResourceAccount(ctx, r.store, app)
	r.seen[key] = reaperAccount{account: account, err: err}
	return account, err
}

// liveInstancesForApp is ListInstancesForApp limited to live rows in SQL when
// the store can. Callers still filter by state: the fallback returns history.
func (e *Engine) liveInstancesForApp(ctx context.Context, appID string) ([]state.Instance, error) {
	if reader, ok := e.store.(state.LiveInstancesForAppsReader); ok {
		return reader.ListLiveInstancesForApps(ctx, []string{appID})
	}
	return e.store.ListInstancesForApp(ctx, appID)
}
