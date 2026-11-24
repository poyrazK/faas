package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SnapshotHealthReader is an optional read capability for the operator
// snapshot health report (ADR-20261122135018); Store stays unchanged.
type SnapshotHealthReader interface {
	// OperatorSnapshotInventory returns snapshot rows, largest parked
	// footprint first, at most limit.
	OperatorSnapshotInventory(ctx context.Context, limit int) ([]SnapshotInventoryRow, error)
	// OperatorLiveDeploymentsWithoutSnapshot returns live deployments of
	// active apps with no usable snapshot, those that cannot cold boot first.
	OperatorLiveDeploymentsWithoutSnapshot(ctx context.Context, limit int) ([]UnsnapshottedDeployment, error)
}

// SnapshotInventoryRow is one snapshot with its app and parked footprint:
// SnapshotBytes (allocated mem + vmstate, or logical bytes from a legacy
// writer) plus LayerBytes (the deployment's above-base layer), as
// fcvm_snapshot_fleet_avg_bytes counts it.
type SnapshotInventoryRow struct {
	ID, DeploymentID, AppID, AppSlug, AccountID, DeploymentStatus string
	Tier, FCVersion, CPUTemplate, CPUModel                        string
	Stale, DeletePending                                          bool
	StaleAt, CreatedAt                                            time.Time
	SnapshotBytes, LayerBytes                                     int64
}

// UnsnapshottedDeployment is a live deployment with no usable snapshot.
// ColdBootable is false when it has no rootfs either.
type UnsnapshottedDeployment struct {
	DeploymentID, AppID, AppSlug, AccountID string
	ColdBootable                            bool
	CreatedAt, LastStaleAt                  time.Time
}

func (s *PgStore) OperatorSnapshotInventory(ctx context.Context, limit int) ([]SnapshotInventoryRow, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().OperatorSnapshotInventory(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("state: operator snapshot inventory: %w", err)
	}
	out := make([]SnapshotInventoryRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, SnapshotInventoryRow{ID: r.ID, DeploymentID: r.DeploymentID, AppID: r.AppID, AppSlug: r.AppSlug,
			AccountID: r.AccountID, DeploymentStatus: r.DeploymentStatus, Tier: r.Tier, FCVersion: r.FcVersion,
			CPUTemplate: r.CpuTemplate, CPUModel: r.CpuModel, Stale: r.Stale, DeletePending: r.DeletePending,
			StaleAt: r.StaleAt.Time, CreatedAt: r.CreatedAt.Time, SnapshotBytes: r.SnapshotBytes, LayerBytes: r.LayerBytes})
	}
	return out, nil
}

func (s *PgStore) OperatorLiveDeploymentsWithoutSnapshot(ctx context.Context, limit int) ([]UnsnapshottedDeployment, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().OperatorLiveDeploymentsWithoutSnapshot(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("state: operator live deployments without snapshot: %w", err)
	}
	out := make([]UnsnapshottedDeployment, 0, len(rows))
	for _, r := range rows {
		out = append(out, UnsnapshottedDeployment{DeploymentID: r.DeploymentID, AppID: r.AppID, AppSlug: r.AppSlug,
			AccountID: r.AccountID, ColdBootable: r.ColdBootable, CreatedAt: r.CreatedAt.Time, LastStaleAt: r.LastStaleAt.Time})
	}
	return out, nil
}

// snapshotFootprint is a snapshot's allocated bytes, or its logical bytes
// when a legacy writer recorded no allocation.
func snapshotFootprint(sn Snapshot) int64 {
	if sn.StoredBytes > 0 {
		return sn.StoredBytes
	}
	return sn.MemBytes + sn.DiskBytes
}

func (m *MemStore) OperatorSnapshotInventory(_ context.Context, limit int) ([]SnapshotInventoryRow, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SnapshotInventoryRow
	for _, sn := range m.snapshots {
		d, ok := m.deployments[sn.DeploymentID]
		if !ok {
			continue
		}
		a, ok := m.apps[d.AppID]
		if !ok {
			continue
		}
		tier := sn.Tier
		if tier == "" {
			tier = SnapshotTierInit
		}
		out = append(out, SnapshotInventoryRow{ID: sn.ID, DeploymentID: sn.DeploymentID, AppID: a.ID, AppSlug: a.Slug,
			AccountID: a.AccountID, DeploymentStatus: string(d.Status), Tier: tier, FCVersion: sn.FCVersion,
			CPUTemplate: sn.CPUTemplate, CPUModel: sn.CPUModel, Stale: sn.Stale, DeletePending: sn.DeletePending,
			CreatedAt: sn.CreatedAt, SnapshotBytes: snapshotFootprint(sn), LayerBytes: d.RootfsBytes})
	}
	sort.SliceStable(out, func(i, j int) bool {
		fi, fj := out[i].SnapshotBytes+out[i].LayerBytes, out[j].SnapshotBytes+out[j].LayerBytes
		if fi != fj {
			return fi > fj
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) OperatorLiveDeploymentsWithoutSnapshot(_ context.Context, limit int) ([]UnsnapshottedDeployment, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	usable := map[string]bool{}
	for _, sn := range m.snapshots {
		if !sn.Stale && !sn.DeletePending {
			usable[sn.DeploymentID] = true
		}
	}
	var out []UnsnapshottedDeployment
	for _, d := range m.deployments {
		a, ok := m.apps[d.AppID]
		if d.Status != DeployLive || d.DeletedAt != nil || !ok || a.Status != AppActive || usable[d.ID] {
			continue
		}
		out = append(out, UnsnapshottedDeployment{DeploymentID: d.ID, AppID: a.ID, AppSlug: a.Slug, AccountID: a.AccountID,
			ColdBootable: d.RootfsKey != "", CreatedAt: d.CreatedAt})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ColdBootable != out[j].ColdBootable {
			return !out[i].ColdBootable
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].DeploymentID < out[j].DeploymentID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
