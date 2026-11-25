package imaged

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// errManagedValkeyImage refuses any image but the pinned Valkey image on a
// managed cache app (ADR-20261116192126).
var errManagedValkeyImage = errors.New("managed Redis caches run only the platform Valkey image")

// managedValkeyImageCheck fails a deployment of a managed cache app whose
// image is not one of api.ManagedValkeyImages. Apps that are not caches, and stores
// without the managed-cache surface, pass.
func (h *Handler) managedValkeyImageCheck(ctx context.Context, app state.App, ref string) error {
	store, ok := h.store.(state.ManagedValkeyStore)
	if !ok {
		return nil
	}
	_, err := store.GetManagedValkeyCache(ctx, app.ID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("imaged: look up managed cache for app %s: %w", app.ID, err)
	case !api.IsManagedValkeyImage(ref):
		return fmt.Errorf("%w: got %s; resize or recreate the cache with `gregale redis` instead of deploying to it", errManagedValkeyImage, ref)
	}
	return nil
}

// isManagedCacheDeployment reports whether deploymentID belongs to a managed
// cache. A cache's park captures its memory, and that capture must replace
// the deployment's live row: keeping the older row, as for every other app,
// would wake the cache with old or no data (ADR-20261116192126).
func (h *Handler) isManagedCacheDeployment(ctx context.Context, deploymentID string) bool {
	store, ok := h.store.(state.ManagedValkeyStore)
	if !ok {
		return false
	}
	dep, err := h.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return false
	}
	_, err = store.GetManagedValkeyCache(ctx, dep.AppID)
	return err == nil
}
