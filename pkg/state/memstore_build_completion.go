package state

import (
	"context"
	"fmt"
	"sort"
	"time"
)

func (m *MemStore) CompleteBuild(_ context.Context, claim Build, path, key string, bytes int64, prov BuildProvenance) error {
	if claim.ID == "" || claim.StartedAt.IsZero() || path == "" || bytes <= 0 || prov.BuildID != claim.ID {
		return fmt.Errorf("state: complete build: invalid claim or artifact")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[claim.ID]
	if !ok || b.Status != BuildRunning || b.DeploymentID != claim.DeploymentID || !b.StartedAt.Equal(claim.StartedAt) {
		return ErrNotFound
	}
	dep, ok := m.deployments[claim.DeploymentID]
	if !ok || (dep.Status != DeployPending && dep.Status != DeployBuilding) {
		return ErrNotFound
	}
	dep.RootfsPath, dep.RootfsKey, dep.RootfsBytes = path, key, bytes
	b.Status, b.FinishedAt = BuildSucceeded, time.Now()
	if prov.SBOMStorageKey == "" {
		prov.SBOMStorageKey = m.buildProvenance[claim.ID].SBOMStorageKey
	}
	m.deployments[dep.ID] = dep
	m.builds[b.ID] = b
	m.buildProvenance[b.ID] = prov
	return nil
}

func (m *MemStore) ListBuildsAwaitingImage(_ context.Context, nodeID string, limit int) ([]BuildImageWork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var builds []Build
	for _, b := range m.builds {
		dep, ok := m.deployments[b.DeploymentID]
		if !ok || (dep.Status != DeployPending && dep.Status != DeployBuilding) || dep.RootfsPath == "" || b.Status != BuildSucceeded {
			continue
		}
		prov, ok := m.buildProvenance[b.ID]
		if !ok || (nodeID != "" && prov.BuilderNodeID != "" && prov.BuilderNodeID != nodeID) {
			continue
		}
		builds = append(builds, b)
	}
	sort.Slice(builds, func(i, j int) bool {
		if builds[i].FinishedAt.Equal(builds[j].FinishedAt) {
			return builds[i].ID < builds[j].ID
		}
		return builds[i].FinishedAt.Before(builds[j].FinishedAt)
	})
	if limit >= 0 && len(builds) > limit {
		builds = builds[:limit]
	}
	var out []BuildImageWork
	for _, b := range builds {
		dep := m.deployments[b.DeploymentID]
		out = append(out, BuildImageWork{AppID: dep.AppID, DeploymentID: dep.ID, NodeID: m.buildProvenance[b.ID].BuilderNodeID})
	}
	return out, nil
}

func (m *MemStore) FailBuild(_ context.Context, claim Build, fc FailureClass, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[claim.ID]
	if !ok || b.Status != BuildRunning || b.DeploymentID != claim.DeploymentID || !b.StartedAt.Equal(claim.StartedAt) {
		return ErrNotFound
	}
	d, ok := m.deployments[claim.DeploymentID]
	if !ok || (d.Status != DeployPending && d.Status != DeployBuilding) {
		return ErrNotFound
	}
	b.Status, b.FailureClass, b.FinishedAt = BuildFailed, fc, time.Now()
	d.Status, d.Error = DeployFailed, message
	m.builds[b.ID], m.deployments[d.ID] = b, d
	return nil
}
