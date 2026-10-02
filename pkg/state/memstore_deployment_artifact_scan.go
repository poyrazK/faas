package state

// adr: 429

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ DeploymentArtifactScanStore = (*MemStore)(nil)

func (m *MemStore) PublishDeploymentArtifactScan(ctx context.Context, input DeploymentArtifactScanInput) (DeploymentArtifactScan, error) {
	in, hash, err := prepareDeploymentArtifactScan(input)
	if err != nil {
		return DeploymentArtifactScan{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DeploymentArtifactScan{}, err
	}
	now := time.Now().UTC()
	parents, err := m.artifactScanParentsLocked(in, now)
	if err != nil {
		return DeploymentArtifactScan{}, err
	}
	root, parent, dep := parents.Rootfs, parents.Approval, parents.Deployment
	pointer := in.DeploymentID + "\x00" + in.WorkloadName
	if old, exists := m.deploymentArtifactScans[in.ID]; exists {
		if old.InputHash != hash || m.deploymentArtifactScanCurrent[pointer] != old.ID {
			return DeploymentArtifactScan{}, ErrConflict
		}
		if err := validateDeploymentArtifactScan(old); err != nil {
			return DeploymentArtifactScan{}, err
		}
		return cloneDeploymentArtifactScan(old), nil
	}
	if err := checkArtifactScanFreshness(in, now); err != nil {
		return DeploymentArtifactScan{}, err
	}
	expires := now.Add(api.ApplicationStandardArtifactScanTTL)
	parentExpiry := parent.ExpiresAt
	if in.RegistryVerificationID == "" {
		parentExpiry = root.ExpiresAt
	}
	if expires.After(parentExpiry) {
		expires = parentExpiry
	}
	value := DeploymentArtifactScan{ID: in.ID, InputHash: hash, Input: in, ScannedAt: now, ExpiresAt: expires, Result: artifactScanResult(in, now)}
	raw, err := json.Marshal(value.Result)
	if err != nil {
		return DeploymentArtifactScan{}, err
	}
	if m.deploymentArtifactScans == nil {
		m.deploymentArtifactScans = map[string]DeploymentArtifactScan{}
	}
	if m.deploymentArtifactScanCurrent == nil {
		m.deploymentArtifactScanCurrent = map[string]string{}
	}
	if in.WorkloadName == "" {
		dep.ScanResult, dep.ScanStatus, dep.ScannedAt = raw, in.Status, now
		m.deployments[dep.ID] = dep
	}
	m.deploymentArtifactScans[in.ID] = value
	m.deploymentArtifactScanCurrent[pointer] = in.ID
	return cloneDeploymentArtifactScan(value), nil
}
func (m *MemStore) GetCurrentDeploymentArtifactScan(ctx context.Context, accountID, appID, depID, workload string) (DeploymentArtifactScan, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, depID) || workload != "" && !api.ValidSidecarName(workload) {
		return DeploymentArtifactScan{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DeploymentArtifactScan{}, err
	}
	return m.currentDeploymentArtifactScanLocked(accountID, appID, depID, workload)
}

func (m *MemStore) artifactScanParentsLocked(in DeploymentArtifactScanInput, now time.Time) (artifactScanParents, error) {
	app, found, dep, exists := m.registryVerificationOwnerLocked(in.AppID, in.DeploymentID)
	if !found || !exists {
		return artifactScanParents{}, ErrNotFound
	}
	root, ok := m.deploymentRegistryRootfs[in.RootfsProducerID]
	if !ok {
		return artifactScanParents{}, ErrNotFound
	}
	pointer := in.DeploymentID + "\x00" + in.WorkloadName
	if m.deploymentRegistryRootfsCurrent[pointer] != root.ID {
		return artifactScanParents{}, ErrApplicationStandardRuntimeStale
	}
	origin, ok := m.deploymentRegistryVerifications[root.Input.RegistryVerificationID]
	if !ok {
		return artifactScanParents{}, ErrNotFound
	}
	parent, ok := m.deploymentRegistryVerifications[artifactScanApprovalID(in, root)]
	if !ok {
		return artifactScanParents{}, ErrNotFound
	}
	if err := checkArtifactScanParent(in, root, origin, parent, dep, now); err != nil {
		return artifactScanParents{}, err
	}
	if err := checkRegistryVerificationOwner(parent.Input, app, dep); err != nil {
		return artifactScanParents{}, err
	}
	if !registryRootfsMatchesMetadata(root, dep, m.deploymentSidecarLayers[dep.ID+"\x00"+in.WorkloadName], origin) {
		return artifactScanParents{}, ErrApplicationStandardRuntimeStale
	}
	signer := m.trustedSigners[trustedSignerKey{AppID: app.ID, SignerName: parent.Input.Proof.PublisherName}]
	if !sameStandardUUID(signer.AccountID, in.AccountID) {
		signer.CosignPublicKey = nil
	}
	if err := verifyRegistryCurrentKey(parent.Input, signer.CosignPublicKey); err != nil {
		return artifactScanParents{}, err
	}
	if err := m.checkArtifactScanBaseLocked(root, parent); err != nil {
		return artifactScanParents{}, err
	}
	return artifactScanParents{Rootfs: root, Approval: parent, Deployment: dep}, nil
}

func (m *MemStore) checkArtifactScanBaseLocked(root DeploymentRegistryRootfs, parent DeploymentRegistryVerification) error {
	if root.Input.BaseProducerID == "" {
		return nil
	}
	base, ok := m.baseImageProducers[root.Input.BaseProducerID]
	if !ok || m.baseImageProducerCurrent[base.Input.Artifact.StorageKey] != base.ID {
		return ErrApplicationStandardRuntimeStale
	}
	return checkRegistryRootfsBase(root.Input, parent, base)
}

func (m *MemStore) currentDeploymentArtifactScanLocked(accountID, appID, depID, workload string) (DeploymentArtifactScan, error) {
	app, found, dep, exists := m.registryVerificationOwnerLocked(appID, depID)
	if !found || !exists || app.Status == AppDeleted || !sameStandardUUID(app.AccountID, accountID) || !sameStandardUUID(dep.AppID, appID) {
		return DeploymentArtifactScan{}, ErrNotFound
	}
	pointer := canonicalStandardUUID(depID) + "\x00" + workload
	value, ok := m.deploymentArtifactScans[m.deploymentArtifactScanCurrent[pointer]]
	if !ok || m.deploymentRegistryRootfsCurrent[pointer] != value.Input.RootfsProducerID || value.Input.OrgID != registryCanonicalOrg(app.OrgID) || value.Input.Scope != dep.Scope {
		return DeploymentArtifactScan{}, ErrNotFound
	}
	root, ok := m.deploymentRegistryRootfs[value.Input.RootfsProducerID]
	if !ok || value.Input.RootfsInputHash != root.InputHash {
		return DeploymentArtifactScan{}, ErrNotFound
	}
	parent, ok := m.deploymentRegistryVerifications[root.Input.RegistryVerificationID]
	if !ok || !registryRootfsMatchesMetadata(root, dep, m.deploymentSidecarLayers[dep.ID+"\x00"+workload], parent) {
		return DeploymentArtifactScan{}, ErrNotFound
	}
	ref, err := registryWorkloadReference(dep, workload)
	if err != nil || ref != value.Input.ImageReference {
		return DeploymentArtifactScan{}, ErrNotFound
	}
	if err := validateDeploymentArtifactScan(value); err != nil {
		return DeploymentArtifactScan{}, err
	}
	return cloneDeploymentArtifactScan(value), nil
}
