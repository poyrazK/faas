package builderd

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// devWatch returns the watch-mode setting of a developer app (ADR-1170),
// or the zero setting for every other app and for stores without the
// capability. Production apps, PR previews and named environments never
// build a watch-mode image, whatever is stored for them.
func devWatch(ctx context.Context, store state.Store, app state.App) (state.DevWatchSetting, error) {
	if !state.IsDeveloperApp(app) || app.Type == state.AppTypeFunction {
		return state.DevWatchSetting{}, nil
	}
	watch, ok := store.(state.DevWatchStore)
	if !ok {
		return state.DevWatchSetting{}, nil
	}
	return watch.DevWatch(ctx, app.ID)
}
