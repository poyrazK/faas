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
// image is not api.ManagedValkeyImage. Apps that are not caches, and stores
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
	case ref != api.ManagedValkeyImage:
		return fmt.Errorf("%w: got %s; resize or recreate the cache with `gregale redis` instead of deploying to it", errManagedValkeyImage, ref)
	}
	return nil
}
