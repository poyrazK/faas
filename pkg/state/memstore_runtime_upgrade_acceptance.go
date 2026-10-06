package state

import (
	"context"
	"time"
)

var _ RuntimeUpgradeAcceptanceStore = (*MemStore)(nil)

func (m *MemStore) prepareRuntimeUpgradeAcceptanceLocked(p RuntimeInstancePublication) (*RuntimeUpgradeAcceptance, error) {
	if p.RuntimeUpgradeColdBoot == nil {
		return nil, nil
	}
	candidate := m.deployments[p.Fence.DeploymentID]
	baseline, captured := m.runtimeUpgradeBaselines[candidate.ID]
	pin, pinned := m.runtimeUpgradeTargets[candidate.ID]
	if !captured || !pinned || !pin.matches(candidate) {
		return nil, ErrConflict
	}
	if _, exists := m.runtimeUpgradeAcceptances[candidate.ID]; exists {
		return nil, ErrConflict // a new boot needs a new candidate, never a refreshed receipt
	}
	current, err := m.runtimeUpgradeBaselineLocked(candidate, baseline.ServingDeploymentID)
	if err != nil || !sameRuntimeUpgradeBaseline(baseline, current) {
		return nil, ErrConflict
	}
	app, target := m.apps[candidate.AppID], m.runtimeReleases[pin.ReleaseID]
	if validateRuntimeUpgradeTarget(app, candidate, target) != nil ||
		m.runtimeArtifactBindings[runtimeArtifactBindingKey(app.AccountID, candidate.RootfsKey)] != target.ID {
		return nil, ErrConflict
	}
	acceptance, err := newRuntimeUpgradeAcceptance(p, candidate, target, m.runtimeReleaseQualifications[target.ID], time.Now().UTC())
	return &acceptance, err
}

func (m *MemStore) DeploymentRuntimeUpgradeAcceptance(_ context.Context, id string) (RuntimeUpgradeAcceptance, error) {
	if validateRuntimeAppEnvIDs(id, id, id) != nil {
		return RuntimeUpgradeAcceptance{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	acceptance, ok := m.runtimeUpgradeAcceptances[id]
	if !ok {
		return RuntimeUpgradeAcceptance{}, ErrNotFound
	}
	return acceptance, nil
}

func (m *MemStore) ValidateDeploymentRuntimeUpgradeAcceptance(_ context.Context, id string) error {
	if validateRuntimeAppEnvIDs(id, id, id) != nil {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.runtimeUpgradeAcceptances[id]
	if !ok {
		return ErrNotFound
	}
	candidate := m.deployments[id]
	baseline, captured := m.runtimeUpgradeBaselines[id]
	pin, pinned := m.runtimeUpgradeTargets[id]
	if !captured || !pinned || !pin.matches(candidate) {
		return ErrConflict
	}
	current, err := m.runtimeUpgradeBaselineLocked(candidate, baseline.ServingDeploymentID)
	if err != nil || !sameRuntimeUpgradeBaseline(baseline, current) {
		return ErrConflict
	}
	app, target := m.apps[candidate.AppID], m.runtimeReleases[pin.ReleaseID]
	if m.runtimeArtifactBindings[runtimeArtifactBindingKey(app.AccountID, candidate.RootfsKey)] != target.ID {
		return ErrConflict
	}
	values, err := m.runtimeAppValuesLocked(app.AccountID, app.ID, candidate.ID)
	if err != nil {
		return ErrConflict
	}
	fence, err := NewRuntimeAppConfigFence(values)
	if err != nil {
		return err
	}
	return validateRuntimeUpgradeAcceptance(a, candidate, target, m.runtimeReleaseQualifications[target.ID], fence, time.Now().UTC())
}
