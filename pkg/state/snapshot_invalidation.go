package state

import (
	"context"
	"errors"
	"fmt"
)

// InvalidateAppSnapshots marks every restorable snapshot for an app stale.
// Snapshots are process-memory caches, so runtime configuration changes must
// invalidate every deployment and both tiers before a later wake can be
// allowed to restore.
//
// It stamps the app's runtime-config change time first (issue #3360). The
// stamp is what stops a live instance that still holds the old environment
// from being captured into a fresh snapshot on its next idle park; the
// invalidation below only covers snapshots that already exist.
func InvalidateAppSnapshots(ctx context.Context, store Store, appID string) (int, error) {
	if err := store.MarkAppRuntimeConfigChanged(ctx, appID); err != nil {
		return 0, fmt.Errorf("mark runtime config changed: %w", err)
	}
	deployments, err := store.ListDeploymentsForApp(ctx, appID, 0, 0)
	if errors.Is(err, ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("list deployments: %w", err)
	}
	invalidated := 0
	for _, deployment := range deployments {
		for _, tier := range []string{SnapshotTierWarm, SnapshotTierInit} {
			n, err := invalidateDeploymentSnapshotTier(ctx, store, deployment.ID, tier)
			invalidated += n
			if err != nil {
				return invalidated, err
			}
		}
	}
	return invalidated, nil
}

func invalidateDeploymentSnapshotTier(ctx context.Context, store Store, deploymentID, tier string) (int, error) {
	invalidated := 0
	for {
		snapshot, err := store.LatestSnapshotForTier(ctx, deploymentID, tier)
		if errors.Is(err, ErrNotFound) {
			return invalidated, nil
		}
		if err != nil {
			return invalidated, fmt.Errorf("latest %s snapshot for deployment %s: %w", tier, deploymentID, err)
		}
		if snapshot.ID == "" {
			return invalidated, nil
		}
		if err := store.MarkSnapshotStale(ctx, snapshot.ID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return invalidated, nil
			}
			return invalidated, fmt.Errorf("mark %s snapshot %s stale: %w", tier, snapshot.ID, err)
		}
		invalidated++
	}
}
