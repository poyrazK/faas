package state

// adr: 435

import (
	"context"
	"errors"
	"time"
)

var _ DeploymentArtifactScanEvidenceStore = (*MemStore)(nil)

func (m *MemStore) freshArtifactScanLocked(accountID, appID, depID, workload string, now time.Time) (DeploymentArtifactScan, artifactScanParents, error) {
	value, err := m.currentDeploymentArtifactScanLocked(accountID, appID, depID, workload)
	if err != nil {
		return DeploymentArtifactScan{}, artifactScanParents{}, err
	}
	parents, err := m.artifactScanParentsLocked(value.Input, now)
	if err != nil {
		return DeploymentArtifactScan{}, parents, err
	}
	if err := checkDeploymentArtifactScanLease(value, parents, now); err != nil {
		return DeploymentArtifactScan{}, parents, err
	}
	return value, parents, nil
}

func (m *MemStore) GetFreshDeploymentArtifactScan(ctx context.Context, accountID, appID, depID, workload string) (DeploymentArtifactScan, error) {
	if !validArtifactScanEvidenceRead(accountID, appID, depID, workload) {
		return DeploymentArtifactScan{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DeploymentArtifactScan{}, err
	}
	value, _, err := m.freshArtifactScanLocked(accountID, appID, depID, workload, time.Now().UTC())
	return value, err
}

func (m *MemStore) GetFreshDeploymentArtifactScanEvidence(ctx context.Context, accountID, appID, depID string) (DeploymentArtifactScanEvidence, error) {
	if !validArtifactScanEvidenceRead(accountID, appID, depID, "") {
		return DeploymentArtifactScanEvidence{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DeploymentArtifactScanEvidence{}, err
	}
	dep, err := m.artifactEvidenceOwnerLocked(accountID, appID, depID)
	if err != nil {
		return DeploymentArtifactScanEvidence{}, err
	}
	value, err := m.artifactEvidenceComponentsLocked(accountID, appID, dep, time.Now().UTC())
	if err != nil {
		return DeploymentArtifactScanEvidence{}, err
	}
	if err := finishArtifactScanEvidence(&value, time.Now().UTC()); err != nil {
		return DeploymentArtifactScanEvidence{}, err
	}
	return value, nil
}

func (m *MemStore) artifactEvidenceOwnerLocked(accountID, appID, depID string) (Deployment, error) {
	app, found, dep, exists := m.registryVerificationOwnerLocked(appID, depID)
	if !found || !exists || app.Status == AppDeleted || !sameStandardUUID(app.AccountID, accountID) || !sameStandardUUID(dep.AppID, appID) {
		return Deployment{}, ErrNotFound
	}
	for _, producer := range m.deploymentRegistryRootfs {
		if sameStandardUUID(producer.Input.DeploymentID, depID) {
			return dep, nil
		}
	}
	return Deployment{}, ErrDeploymentArtifactScanEvidenceAbsent
}

func (m *MemStore) artifactEvidenceComponentsLocked(accountID, appID string, dep Deployment, now time.Time) (DeploymentArtifactScanEvidence, error) {
	names, err := artifactScanWorkloads(dep.Sidecars)
	if err != nil {
		return DeploymentArtifactScanEvidence{}, err
	}
	value := DeploymentArtifactScanEvidence{Components: []DeploymentArtifactScan{}, Bases: []BaseImageScan{}}
	bases := map[string]bool{}
	for _, name := range names {
		scan, parents, err := m.freshArtifactScanLocked(accountID, appID, dep.ID, name, now)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				err = ErrApplicationStandardRuntimeStale
			}
			return DeploymentArtifactScanEvidence{}, err
		}
		value.Components = append(value.Components, scan)
		value.Artifacts = append(value.Artifacts, runtimeArtifactFromRootfs(parents.Rootfs))
		baseID := parents.Rootfs.Input.BaseProducerID
		if baseID != "" && !bases[baseID] {
			base, err := m.freshBaseImageScanLocked(baseID, parents.Rootfs.Input.BaseInputHash, now)
			if err != nil {
				return DeploymentArtifactScanEvidence{}, err
			}
			value.Bases, bases[baseID] = append(value.Bases, base), true
			value.Artifacts = append(value.Artifacts, runtimeArtifactFromBaseScan(base))
		}
	}
	return value, nil
}
