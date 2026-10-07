package state

// adr: 435. Whole-runtime selections share producer publication fences.

import (
	"context"
	"time"
)

var _ DeploymentRuntimeScanStore = (*MemStore)(nil)

func (m *MemStore) PublishDeploymentRuntimeScan(ctx context.Context, input DeploymentRuntimeScanInput) (DeploymentRuntimeScan, error) {
	in, hash, err := prepareDeploymentRuntimeScan(input)
	if err != nil {
		return DeploymentRuntimeScan{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DeploymentRuntimeScan{}, err
	}
	current, err := m.freshRuntimeProducerInputsLocked(ctx, in.AccountID, in.AppID, in.DeploymentID)
	if err != nil {
		return DeploymentRuntimeScan{}, err
	}
	if current.InputHash != in.Facts.InputHash {
		return DeploymentRuntimeScan{}, ErrApplicationStandardRuntimeStale
	}
	if old, exists := m.deploymentRuntimeScans[in.ID]; exists {
		if old.InputHash != hash || m.deploymentRuntimeScanCurrent[in.DeploymentID] != old.ID {
			return DeploymentRuntimeScan{}, ErrConflict
		}
		if err := validateDeploymentRuntimeScan(old); err != nil {
			return DeploymentRuntimeScan{}, err
		}
		if !current.ExpiresAt.After(time.Now().UTC()) {
			return DeploymentRuntimeScan{}, ErrApplicationStandardRuntimeStale
		}
		return cloneDeploymentRuntimeScan(old), ctx.Err()
	}
	now := time.Now().UTC()
	expires, err := runtimeScanDeadline(in, now, current.ExpiresAt)
	if err != nil {
		return DeploymentRuntimeScan{}, err
	}
	if err := ctx.Err(); err != nil {
		return DeploymentRuntimeScan{}, err
	}
	value := DeploymentRuntimeScan{ID: in.ID, InputHash: hash, Input: in, ScannedAt: now, ExpiresAt: expires}
	m.selectRuntimeScanLocked(value)
	return cloneDeploymentRuntimeScan(value), nil
}

func (m *MemStore) selectRuntimeScanLocked(value DeploymentRuntimeScan) {
	if m.deploymentRuntimeScans == nil {
		m.deploymentRuntimeScans = map[string]DeploymentRuntimeScan{}
	}
	if m.deploymentRuntimeScanCurrent == nil {
		m.deploymentRuntimeScanCurrent = map[string]string{}
	}
	m.deploymentRuntimeScans[value.ID] = value
	m.deploymentRuntimeScanCurrent[value.Input.DeploymentID] = value.ID
}

func (m *MemStore) GetCurrentDeploymentRuntimeScan(ctx context.Context, accountID, appID, depID string) (DeploymentRuntimeScan, error) {
	if !validArtifactScanEvidenceRead(accountID, appID, depID, "") {
		return DeploymentRuntimeScan{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DeploymentRuntimeScan{}, err
	}
	app, found, dep, exists := m.registryVerificationOwnerLocked(appID, depID)
	if !found || !exists || app.Status == AppDeleted || !sameStandardUUID(app.AccountID, accountID) || !sameStandardUUID(dep.AppID, appID) {
		return DeploymentRuntimeScan{}, ErrNotFound
	}
	value, ok := m.deploymentRuntimeScans[m.deploymentRuntimeScanCurrent[canonicalStandardUUID(depID)]]
	if !ok {
		return DeploymentRuntimeScan{}, ErrNotFound
	}
	if !sameStandardUUID(value.Input.AccountID, accountID) || !sameStandardUUID(value.Input.AppID, appID) || !sameStandardUUID(value.Input.DeploymentID, depID) {
		return DeploymentRuntimeScan{}, ErrNotFound
	}
	if err := validateDeploymentRuntimeScan(value); err != nil {
		return DeploymentRuntimeScan{}, err
	}
	return cloneDeploymentRuntimeScan(value), ctx.Err()
}

func (m *MemStore) GetFreshDeploymentRuntimeScan(ctx context.Context, accountID, appID, depID string) (DeploymentRuntimeScanEvidence, error) {
	if !validArtifactScanEvidenceRead(accountID, appID, depID, "") {
		return DeploymentRuntimeScanEvidence{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.freshRuntimeScanLocked(ctx, accountID, appID, depID)
}

func (m *MemStore) freshRuntimeScanLocked(ctx context.Context, accountID, appID, depID string) (DeploymentRuntimeScanEvidence, error) {
	if err := ctx.Err(); err != nil {
		return DeploymentRuntimeScanEvidence{}, err
	}
	current, err := m.freshRuntimeProducerInputsLocked(ctx, accountID, appID, depID)
	if err != nil {
		return DeploymentRuntimeScanEvidence{}, err
	}
	value, ok := m.deploymentRuntimeScans[m.deploymentRuntimeScanCurrent[current.DeploymentID]]
	if !ok {
		return DeploymentRuntimeScanEvidence{}, ErrApplicationStandardRuntimeStale
	}
	if err := validateDeploymentRuntimeScan(value); err != nil {
		return DeploymentRuntimeScanEvidence{}, err
	}
	if err := ctx.Err(); err != nil {
		return DeploymentRuntimeScanEvidence{}, err
	}
	current.CheckedAt = time.Now().UTC()
	return finishRuntimeScanEvidence(value, current)
}

func (m *MemStore) deleteAppRuntimeScansLocked(appID string) {
	for id, scan := range m.deploymentRuntimeScans {
		if sameStandardUUID(scan.Input.AppID, appID) {
			delete(m.deploymentRuntimeScans, id)
			delete(m.deploymentRuntimeScanCurrent, scan.Input.DeploymentID)
		}
	}
}
