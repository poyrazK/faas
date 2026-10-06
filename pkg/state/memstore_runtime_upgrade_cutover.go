package state

import (
	"context"
	"time"
)

var _ RuntimeUpgradeCutoverStore = (*MemStore)(nil)

func (m *MemStore) CutoverDeploymentRuntimeUpgrade(ctx context.Context, r RuntimeUpgradeCutoverRequest) (RuntimeUpgradeCutover, error) {
	if err := r.validate(); err != nil {
		return RuntimeUpgradeCutover{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RuntimeUpgradeCutover{}, err
	}
	d, ok := m.deployments[r.DeploymentID]
	app := m.apps[d.AppID]
	if !ok || d.AppID != r.AppID || app.AccountID != r.AccountID || app.Status != AppActive || app.DeletedAt != nil || d.DeletedAt != nil {
		return RuntimeUpgradeCutover{}, ErrConflict
	}
	if old, ok := m.runtimeUpgradeCutovers[d.ID]; ok {
		if !r.matches(old) {
			return RuntimeUpgradeCutover{}, ErrConflict
		}
		return old, nil // historical confirmation; never move traffic again
	}
	if d.Status != DeployLive || d.EnvironmentWorkloadHeld() {
		return RuntimeUpgradeCutover{}, ErrConflict
	}
	if err := m.validateRuntimeUpgradeAcceptanceLocked(d.ID); err != nil {
		return RuntimeUpgradeCutover{}, err
	}
	if err := m.requireLayerArtifactsRetainedLocked(m.deploymentLayerKeysLocked(d)); err != nil {
		return RuntimeUpgradeCutover{}, err
	}
	cutover, err := newRuntimeUpgradeCutover(r, m.runtimeUpgradeAcceptances[d.ID], m.runtimeUpgradeBaselines[d.ID], time.Now())
	if err != nil {
		return RuntimeUpgradeCutover{}, err
	}
	if m.runtimeUpgradeCutovers == nil {
		m.runtimeUpgradeCutovers = make(map[string]RuntimeUpgradeCutover)
	}
	// All fallible checks precede both the receipt and the traffic mutation.
	m.runtimeUpgradeCutovers[d.ID] = cutover
	serving := m.deployments[cutover.ServingDeploymentID]
	serving.TrafficPercent = 0
	d.TrafficPercent = 100
	m.putDeploymentLocked(serving.ID, serving)
	m.putDeploymentLocked(d.ID, d)
	return cutover, nil
}

func (m *MemStore) DeploymentRuntimeUpgradeCutover(_ context.Context, id string) (RuntimeUpgradeCutover, error) {
	if validateRuntimeAppEnvIDs(id, id, id) != nil {
		return RuntimeUpgradeCutover{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.runtimeUpgradeCutovers[id]
	if !ok {
		return RuntimeUpgradeCutover{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) runtimeUpgradeTrafficAllowedLocked(d Deployment) bool {
	pin, pinned := m.runtimeUpgradeTargets[d.ID]
	if !pinned {
		return true
	}
	c, applied := m.runtimeUpgradeCutovers[d.ID]
	a := m.runtimeUpgradeAcceptances[d.ID]
	return applied && c.TargetReleaseID == pin.ReleaseID && c.TargetReleaseID == a.TargetReleaseID &&
		c.WakeID == a.WakeID && c.QualificationReportSHA256 == a.QualificationReportSHA256 && a.RootfsKey == d.RootfsKey
}

func (m *MemStore) checkRuntimeUpgradeTrafficLocked(d Deployment) error {
	if d.Status == DeployLive && d.TrafficPercent > 0 && !m.runtimeUpgradeTrafficAllowedLocked(d) {
		return ErrConflict
	}
	return nil
}

// Recovery/service orchestration may choose any zero-weight live predecessor.
// Refuse the operation before side effects when such a choice could activate
// an unapproved upgrade. Failure fallback instead explicitly skips it.
func (m *MemStore) checkRuntimeUpgradeRecoveryLocked(appID string) error {
	for _, d := range m.deployments {
		if d.AppID == appID && d.Status == DeployLive && !m.runtimeUpgradeTrafficAllowedLocked(d) {
			return ErrConflict
		}
	}
	return nil
}
