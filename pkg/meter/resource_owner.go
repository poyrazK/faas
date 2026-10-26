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
