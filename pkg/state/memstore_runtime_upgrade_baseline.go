package state

import (
	"context"
	"time"
)

var _ RuntimeUpgradeBaselineStore = (*MemStore)(nil)

func (m *MemStore) CaptureDeploymentRuntimeUpgradeBaseline(_ context.Context, deploymentID, servingID string) (RuntimeUpgradeBaseline, error) {
	if validateRuntimeAppEnvIDs(deploymentID, servingID, deploymentID) != nil {
		return RuntimeUpgradeBaseline{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	candidate, ok := m.deployments[deploymentID]
	if !ok {
		return RuntimeUpgradeBaseline{}, ErrNotFound
	}
	if candidate.Status != DeployPending || candidate.RootfsKey != "" || candidate.RootfsPath != "" || candidate.ImageDigest != "" ||
		candidate.TrafficPercent != 0 || !candidate.TrafficPercentExplicit {
		return RuntimeUpgradeBaseline{}, ErrConflict
	}
	for _, build := range m.builds {
		if build.DeploymentID == deploymentID {
			return RuntimeUpgradeBaseline{}, ErrConflict
		}
	}
	current, err := m.runtimeUpgradeBaselineLocked(candidate, servingID)
	if err != nil {
		return RuntimeUpgradeBaseline{}, err
	}
	if old, ok := m.runtimeUpgradeBaselines[deploymentID]; ok {
		if !sameRuntimeUpgradeBaseline(old, current) {
			return RuntimeUpgradeBaseline{}, ErrConflict
		}
		return old, nil
	}
	current.CapturedAt = time.Now().UTC()
	if m.runtimeUpgradeBaselines == nil {
		m.runtimeUpgradeBaselines = map[string]RuntimeUpgradeBaseline{}
	}
	m.runtimeUpgradeBaselines[deploymentID] = current
	return current, nil
}

func (m *MemStore) runtimeUpgradeBaselineLocked(candidate Deployment, servingID string) (RuntimeUpgradeBaseline, error) {
	pin, pinned := m.runtimeUpgradeTargets[candidate.ID]
	if !pinned || !pin.matches(candidate) {
		return RuntimeUpgradeBaseline{}, ErrConflict
	}
	return m.runtimeUpgradeBaselineForTargetLocked(candidate, servingID, pin)
}

func (m *MemStore) runtimeUpgradeBaselineForTargetLocked(candidate Deployment, servingID string, pin runtimeUpgradeTarget) (RuntimeUpgradeBaseline, error) {
	serving, ok := m.deployments[servingID]
	app := m.apps[candidate.AppID]
	if !ok || !pin.matches(candidate) {
		return RuntimeUpgradeBaseline{}, ErrConflict
	}
	deployments := make([]Deployment, 0, len(m.deployments))
	for _, dep := range m.deployments {
		deployments = append(deployments, dep)
	}
	if !runtimeUpgradeServingStable(deployments, candidate, serving) {
		return RuntimeUpgradeBaseline{}, ErrConflict
	}
	boundID := m.runtimeArtifactBindings[runtimeArtifactBindingKey(app.AccountID, serving.RootfsKey)]
	target, targetOK := m.runtimeReleases[pin.ReleaseID]
	current, currentOK := m.runtimeReleases[boundID]
	if !targetOK || !currentOK {
		return RuntimeUpgradeBaseline{}, ErrConflict
	}
	candidateValues, err := m.runtimeAppValuesLocked(app.AccountID, app.ID, candidate.ID)
	if err != nil {
		return RuntimeUpgradeBaseline{}, ErrConflict
	}
	servingValues, err := m.runtimeAppValuesLocked(app.AccountID, app.ID, serving.ID)
	if err != nil {
		return RuntimeUpgradeBaseline{}, ErrConflict
	}
	return newRuntimeUpgradeBaseline(app, candidate, serving, target, current, candidateValues, servingValues)
}

func (m *MemStore) DeploymentRuntimeUpgradeBaseline(_ context.Context, deploymentID string) (RuntimeUpgradeBaseline, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	baseline, ok := m.runtimeUpgradeBaselines[deploymentID]
	if !ok {
		return RuntimeUpgradeBaseline{}, ErrNotFound
	}
	return baseline, nil
}

func (m *MemStore) ValidateDeploymentRuntimeUpgradeBaseline(_ context.Context, deploymentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	baseline, ok := m.runtimeUpgradeBaselines[deploymentID]
	if !ok {
		return ErrNotFound
	}
	current, err := m.runtimeUpgradeBaselineLocked(m.deployments[deploymentID], baseline.ServingDeploymentID)
	if err != nil || !sameRuntimeUpgradeBaseline(baseline, current) {
		return ErrConflict
	}
	return nil
}
