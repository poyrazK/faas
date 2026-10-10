package builderd

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// devWatchCommand returns the watch-mode command of a developer app
// (ADR-970), or "" for every other app and for stores without the
// capability. Production apps, PR previews and named environments never
// build a watch-mode image, whatever is stored for them.
func devWatchCommand(ctx context.Context, store state.Store, app state.App) (string, error) {
	if !state.IsDeveloperApp(app) || app.Type == state.AppTypeFunction {
		return "", nil
	}
	watch, ok := store.(state.DevWatchStore)
	if !ok {
		return "", nil
	}
	return watch.DevWatchCommand(ctx, app.ID)
}
