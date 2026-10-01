package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ComputeRegion is durable operator policy. Node health and spare capacity are
// observations, never stored as region identity or admission policy.
type ComputeRegion struct {
	ID               string
	DisplayName      string
	AcceptingNewApps bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func validComputeRegion(region ComputeRegion) bool {
	return region.ID != "" && api.ValidComputeRegion(region.ID) && api.ValidComputeRegionDisplayName(region.DisplayName)
}

// Bootstrap mirrors the registry migration's backfill of the synthetic node.
// Ordinary node registration never creates or changes region policy.
func (m *MemStore) seedComputeRegionsLocked() {
	for _, node := range m.computeNodes {
		if node.Region == nil || *node.Region == "" || !api.ValidComputeRegion(*node.Region) {
			continue
		}
		id := *node.Region
		m.computeRegions[id] = ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true, CreatedAt: node.CreatedAt, UpdatedAt: node.CreatedAt}
	}
}

func (m *MemStore) UpsertComputeRegion(_ context.Context, region ComputeRegion) (ComputeRegion, error) {
	if !validComputeRegion(region) {
		return ComputeRegion{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, exists := m.computeRegions[region.ID]
	region.CreatedAt = old.CreatedAt
	if !exists {
		region.CreatedAt = time.Now()
	}
	region.UpdatedAt = time.Now()
	m.computeRegions[region.ID] = region
	return region, nil
}

func (m *MemStore) ListComputeRegions(_ context.Context) ([]ComputeRegion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ComputeRegion, 0, len(m.computeRegions))
	for _, region := range m.computeRegions {
		out = append(out, region)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *MemStore) ComputeRegionByID(_ context.Context, id string) (ComputeRegion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	region, ok := m.computeRegions[id]
	if !ok {
		return ComputeRegion{}, ErrNotFound
	}
	return region, nil
}

func (s *PgStore) UpsertComputeRegion(ctx context.Context, region ComputeRegion) (ComputeRegion, error) {
	if !validComputeRegion(region) {
		return ComputeRegion{}, ErrInvalidArgument
	}
	row, err := (&sqlc.Queries{}).UpsertComputeRegion(ctx, s.pool, sqlc.UpsertComputeRegionParams{
		ID: region.ID, DisplayName: region.DisplayName, AcceptingNewApps: region.AcceptingNewApps,
	})
	return computeRegionFromSQL(row), mapErr(err)
}

func (s *PgStore) ListComputeRegions(ctx context.Context) ([]ComputeRegion, error) {
	rows, err := (&sqlc.Queries{}).ListComputeRegions(ctx, s.pool)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ComputeRegion, 0, len(rows))
	for _, row := range rows {
		out = append(out, computeRegionFromSQL(row))
	}
	return out, nil
}

func (s *PgStore) ComputeRegionByID(ctx context.Context, id string) (ComputeRegion, error) {
	row, err := (&sqlc.Queries{}).ComputeRegionByID(ctx, s.pool, id)
	return computeRegionFromSQL(row), mapErr(err)
}

func computeRegionFromSQL(row sqlc.ComputeRegion) ComputeRegion {
	return ComputeRegion{ID: row.ID, DisplayName: row.DisplayName, AcceptingNewApps: row.AcceptingNewApps,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}

// ComputeNodePlacementEligible mirrors the chooser's static admission gates.
// It does not promise that a particular request fits the remaining capacity.
func ComputeNodePlacementEligible(node ComputeNode) bool {
	return node.Active && (node.Lifecycle == "" || node.Lifecycle.IsAdmitting()) &&
		node.AdmissionCeilingMB > 0 && node.VCPUBudget > 0
}

func (m *MemStore) checkNewAppComputeRegionLocked(app App) error {
	if app.ComputeRegion != "" {
		region, ok := m.computeRegions[app.ComputeRegion]
		if !ok {
			return api.ErrComputeRegionUnknown(app.ComputeRegion)
		}
		if !region.AcceptingNewApps {
			return api.ErrComputeRegionAdmissionPaused(app.ComputeRegion)
		}
	}
	return m.checkComputeRegionLocked(app, app.NodeID)
}
