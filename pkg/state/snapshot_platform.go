package state

import (
	"context"
	"errors"
	"slices"
)

// Per-platform snapshot slots (ADR-20261024225612 amendment 1).
//
// A deployment keeps one live snapshot per tier per CPU platform, so a cell
// whose nodes cannot share snapshots (Intel and AMD, or template-less nodes
// on different CPUs) keeps a restorable capture for each side instead of the
// two sides replacing each other on every park.

// SnapshotCPUPlatform is the slot a capture occupies among a deployment's
// live rows of one tier: the template when the capturing node used one,
// otherwise its host CPU signature. Rows from before the ADR share the
// empty platform. The unique index snapshots_deployment_tier_platform_key
// computes the same expression.
func SnapshotCPUPlatform(template, model string) string {
	if template != "" {
		return "template:" + template
	}
	return model
}

// CPUPlatform is SnapshotCPUPlatform of the snapshot's capturing node.
func (s Snapshot) CPUPlatform() string { return SnapshotCPUPlatform(s.CPUTemplate, s.CPUModel) }

// LiveSnapshotLister lists every live snapshot of a deployment's tier, one
// per CPU platform, newest first. Stores that cannot list fall back to
// LatestSnapshotForTier, which returns the newest of them.
type LiveSnapshotLister interface {
	LiveSnapshotsForTier(ctx context.Context, deploymentID, tier string) ([]Snapshot, error)
}

// LiveSnapshotsForTier lists the tier's live rows through store, or the one
// LatestSnapshotForTier returns when store cannot list. No live row is an
// empty slice and a nil error.
func LiveSnapshotsForTier(ctx context.Context, store Store, deploymentID, tier string) ([]Snapshot, error) {
	if lister, ok := store.(LiveSnapshotLister); ok {
		return lister.LiveSnapshotsForTier(ctx, deploymentID, tier)
	}
	snap, err := store.LatestSnapshotForTier(ctx, deploymentID, tier)
	switch {
	case err == nil:
		return []Snapshot{snap}, nil
	case errors.Is(err, ErrNotFound):
		return nil, nil
	}
	return nil, err
}

// LiveSnapshotsForTier mirrors PgStore.LiveSnapshotsForTier with the same
// visibility rules as MemStore.LatestSnapshotForTier.
func (m *MemStore) LiveSnapshotsForTier(_ context.Context, deploymentID, tier string) ([]Snapshot, error) {
	if tier == "" {
		tier = SnapshotTierInit
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Snapshot
	for _, s := range m.snapshots {
		if s.DeploymentID != deploymentID || s.Stale || s.Tier != tier || s.RecoveryGeneration != m.snapshotRecoveryGenerations[deploymentID] {
			continue
		}
		if deployment, ok := m.deployments[s.DeploymentID]; !ok ||
			deployment.Status == DeployFailed || deployment.Status == DeployCancelled {
			continue
		} else if app, ok := m.apps[deployment.AppID]; !ok || app.Status == AppDeleted {
			continue
		}
		out = append(out, s)
	}
	slices.SortStableFunc(out, func(a, b Snapshot) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}
