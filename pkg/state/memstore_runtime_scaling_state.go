package state

import (
	"context"
	"time"

	"github.com/google/uuid"
)

func (m *MemStore) RuntimeScalingStateForDeployment(_ context.Context, accountID, appID, deploymentID string) (RuntimeScalingState, error) {
	if err := validateRuntimeAppEnvIDs(accountID, appID, deploymentID); err != nil {
		return RuntimeScalingState{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owner, err := m.runtimeAppValueOwnerLocked(accountID, appID, deploymentID)
	if err != nil {
		return RuntimeScalingState{}, err
	}
	key := appID + "\x00" + runtimeScalingEnvironmentKey(owner.Scope, owner.EnvironmentID)
	row, ok := m.runtimeEnvironmentScalingStates[key]
	if !ok && workloadEnvironmentSlug(owner.Scope) == "production" {
		app := m.apps[appID]
		row.LastScaleInAt, row.LastScaleOutAt = app.LastScaleInAt, app.LastScaleOutAt
	}
	row.RuntimeAppEnvSnapshot = owner
	return copyRuntimeScalingState(row), nil
}

func (m *MemStore) StampDeploymentScaleIn(ctx context.Context, deploymentID string) error {
	return m.stampRuntimeScalingState(ctx, deploymentID, "in")
}
func (m *MemStore) StampDeploymentScaleOut(ctx context.Context, deploymentID string) error {
	return m.stampRuntimeScalingState(ctx, deploymentID, "out")
}

func (m *MemStore) stampRuntimeScalingState(_ context.Context, deploymentID, direction string) error {
	id, err := uuid.Parse(deploymentID)
	if err != nil || id == uuid.Nil {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dep, ok := m.deployments[deploymentID]
	if !ok {
		return ErrNotFound
	}
	app := m.apps[dep.AppID]
	owner, err := m.runtimeAppValueOwnerLocked(app.AccountID, app.ID, dep.ID)
	if err != nil {
		return err
	}
	key := app.ID + "\x00" + runtimeScalingEnvironmentKey(owner.Scope, owner.EnvironmentID)
	row, exists := m.runtimeEnvironmentScalingStates[key]
	if !exists && workloadEnvironmentSlug(owner.Scope) == "production" {
		row.LastScaleInAt, row.LastScaleOutAt = app.LastScaleInAt, app.LastScaleOutAt
	}
	row.RuntimeAppEnvSnapshot = owner
	now := time.Now().UTC()
	if direction == "in" {
		row.LastScaleInAt = &now
	} else {
		row.LastScaleOutAt = &now
	}
	if m.runtimeEnvironmentScalingStates == nil {
		m.runtimeEnvironmentScalingStates = map[string]RuntimeScalingState{}
	}
	m.runtimeEnvironmentScalingStates[key] = copyRuntimeScalingState(row)
	if workloadEnvironmentSlug(owner.Scope) == "production" {
		app.LastScaleInAt, app.LastScaleOutAt = row.LastScaleInAt, row.LastScaleOutAt
		m.apps[app.ID] = app
		for productionKey, productionRow := range m.runtimeEnvironmentScalingStates {
			if productionRow.AppID == app.ID && workloadEnvironmentSlug(productionRow.Scope) == "production" {
				productionRow.LastScaleInAt, productionRow.LastScaleOutAt = app.LastScaleInAt, app.LastScaleOutAt
				m.runtimeEnvironmentScalingStates[productionKey] = copyRuntimeScalingState(productionRow)
			}
		}
	}
	return nil
}

// Unscoped compatibility stamps address production only, including the fixed
// timestamp helpers used by scheduler tests. Stages never inherit them.
func (m *MemStore) stampLegacyProductionScalingLocked(appID, direction string, stamp time.Time) {
	for key, row := range m.runtimeEnvironmentScalingStates {
		if row.AppID != appID || workloadEnvironmentSlug(row.Scope) != "production" {
			continue
		}
		if direction == "in" {
			row.LastScaleInAt = &stamp
		} else {
			row.LastScaleOutAt = &stamp
		}
		m.runtimeEnvironmentScalingStates[key] = copyRuntimeScalingState(row)
	}
}

func (m *MemStore) deleteRuntimeScalingEnvironmentLocked(environmentID string) {
	for key, row := range m.runtimeEnvironmentScalingStates {
		if row.EnvironmentID == environmentID {
			delete(m.runtimeEnvironmentScalingStates, key)
		}
	}
}
