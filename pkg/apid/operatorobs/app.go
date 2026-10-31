package operatorobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// AppSnapshotsReader is the part of the store AppSnapshots reads.
type AppSnapshotsReader interface {
	LiveDeployments(ctx context.Context, appID string) ([]state.Deployment, error)
	LatestSnapshotForTier(ctx context.Context, deploymentID, tier string) (state.Snapshot, error)
}

// appSnapshotTiers is schedd's restore order: warm, then init, then cold boot.
var appSnapshotTiers = []string{state.SnapshotTierWarm, state.SnapshotTierInit}

// AppSnapshots returns, for each live deployment (newest first, at most
// api.ObsAppSnapshotDeploymentsMax), one row per tier: the freshest
// non-stale snapshot, or Present=false when the tier has none. An app with
// no live deployment returns an empty slice.
func AppSnapshots(ctx context.Context, r AppSnapshotsReader, appID string) ([]api.ObsAppSnapshot, error) {
	deployments, err := r.LiveDeployments(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("list live deployments: %w", err)
	}
	if len(deployments) > api.ObsAppSnapshotDeploymentsMax {
		deployments = deployments[:api.ObsAppSnapshotDeploymentsMax]
	}
	out := make([]api.ObsAppSnapshot, 0, len(deployments)*len(appSnapshotTiers))
	for _, d := range deployments {
		for _, tier := range appSnapshotTiers {
			row := api.ObsAppSnapshot{DeploymentID: d.ID, Tier: tier}
			snap, err := r.LatestSnapshotForTier(ctx, d.ID, tier)
			switch {
			case errors.Is(err, state.ErrNotFound):
			case err != nil:
				return nil, fmt.Errorf("latest %s snapshot of deployment %s: %w", tier, d.ID, err)
			default:
				created := snap.CreatedAt.UTC()
				row.Present = true
				row.ID = snap.ID
				row.FCVersion = snap.FCVersion
				row.MemBytes = snap.MemBytes
				row.DiskBytes = snap.DiskBytes
				row.StoredBytes = snap.StoredBytes
				row.CreatedAt = &created
			}
			out = append(out, row)
		}
	}
	return out, nil
}
