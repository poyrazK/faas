package state

import (
	"context"
	"sort"
	"time"
)

// snapshotGCMetadataLocked resolves the original owner without reading secrets
// or adopting the current workload head. The caller holds m.mu.
func (m *MemStore) snapshotGCMetadataLocked(snapshot Snapshot) (SnapshotForGC, bool) {
	deployment, ok := m.deployments[snapshot.DeploymentID]
	if !ok {
		return SnapshotForGC{}, false
	}
	app, ok := m.apps[deployment.AppID]
	if !ok {
		return SnapshotForGC{}, false
	}
	owner, ownerErr := m.runtimeAppValueOwnerLocked(app.AccountID, app.ID, deployment.ID)
	environmentID := m.deploymentRuntimeEnvironmentOwners[deployment.ID]
	if environmentID == "" {
		environmentID = owner.EnvironmentID
	}
	warmEnabled := app.WarmSnapshotEnabled
	if pinID := m.projectEnvironmentWorkloadDeploymentSpecs[deployment.ID]; pinID != "" {
		warmEnabled = m.projectEnvironmentWorkloadSpecs[pinID].Settings.WarmSnapshotEnabled
	}
	return SnapshotForGC{
		ID: snapshot.ID, DeploymentID: deployment.ID, AppID: app.ID, AccountID: app.AccountID,
		AppSlug: app.Slug, AppStatus: app.Status, DeploymentStatus: deployment.Status,
		Scope: normalizedDeploymentScope(deployment.Scope), EnvironmentID: environmentID,
		RuntimeOwnerInvalid: ownerErr != nil, DeploymentRootfsKey: deployment.RootfsKey,
		FCVersion: snapshot.FCVersion, MemBytes: snapshot.MemBytes, DiskBytes: snapshot.DiskBytes,
		Tier: snapshot.Tier, StorageKey: snapshot.StorageKey, Stale: snapshot.Stale,
		DeletePending: snapshot.DeletePending, CreatedAt: snapshot.CreatedAt, AppWarmSnapshotEnabled: warmEnabled,
	}, true
}

func (m *MemStore) ListSnapshotsForGC(_ context.Context) ([]SnapshotForGC, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []SnapshotForGC
	for _, snapshot := range m.snapshots {
		row, ok := m.snapshotGCMetadataLocked(snapshot)
		if ok && (!row.Stale || row.RuntimeOwnerInvalid) {
			rows = append(rows, row)
		}
	}
	sortSnapshotGCRows(rows, true)
	return rows, nil
}

func (m *MemStore) ListSnapshotsStaleOlderThan(_ context.Context, retention time.Duration) ([]SnapshotForGC, error) {
	cutoff := time.Now().Add(-retention)
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []SnapshotForGC
	for _, snapshot := range m.snapshots {
		if !snapshot.Stale || !snapshot.CreatedAt.Before(cutoff) {
			continue
		}
		if row, ok := m.snapshotGCMetadataLocked(snapshot); ok {
			rows = append(rows, row)
		}
	}
	sortSnapshotGCRows(rows, false)
	return rows, nil
}

// Pending deletion has no age cutoff: remote deletion retries must not depend
// on whether a snapshot's timestamp is ahead of this process's clock.
func (m *MemStore) ListSnapshotsPendingDelete(_ context.Context) ([]SnapshotForGC, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []SnapshotForGC
	for _, snapshot := range m.snapshots {
		if !snapshot.DeletePending {
			continue
		}
		if row, ok := m.snapshotGCMetadataLocked(snapshot); ok {
			rows = append(rows, row)
		}
	}
	sortSnapshotGCRows(rows, false)
	return rows, nil
}

func sortSnapshotGCRows(rows []SnapshotForGC, newestFirst bool) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID < rows[j].ID
		}
		if newestFirst {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})
}
