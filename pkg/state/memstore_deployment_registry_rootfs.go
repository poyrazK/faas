package state

// adr: 393

import (
	"context"
	"time"
)

var _ DeploymentRegistryRootfsStore = (*MemStore)(nil)

func (m *MemStore) PublishDeploymentRegistryRootfs(ctx context.Context, input DeploymentRegistryRootfsInput) (DeploymentRegistryRootfs, error) {
	in, hash, err := prepareRegistryRootfs(input)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	app, found, dep, exists := m.registryVerificationOwnerLocked(in.AppID, in.DeploymentID)
	if !found || !exists {
		return DeploymentRegistryRootfs{}, ErrNotFound
	}
	parent, ok := m.deploymentRegistryVerifications[in.RegistryVerificationID]
	if !ok {
		return DeploymentRegistryRootfs{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := checkRegistryRootfsParent(in, parent, dep, now); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	if err := checkRegistryVerificationOwner(parent.Input, app, dep); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	signer := m.trustedSigners[trustedSignerKey{AppID: app.ID, SignerName: parent.Input.Proof.PublisherName}]
	if !sameStandardUUID(signer.AccountID, in.AccountID) {
		signer.CosignPublicKey = nil
	}
	if err := verifyRegistryCurrentKey(parent.Input, signer.CosignPublicKey); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	pointer := in.DeploymentID + "\x00" + in.WorkloadName
	if old, exists := m.deploymentRegistryRootfs[in.ID]; exists {
		if old.InputHash != hash || m.deploymentRegistryRootfsCurrent[pointer] != in.ID {
			return DeploymentRegistryRootfs{}, ErrConflict
		}
		layer := m.deploymentSidecarLayers[dep.ID+"\x00"+in.WorkloadName]
		if !registryRootfsMatchesMetadata(old, dep, layer, parent) {
			return DeploymentRegistryRootfs{}, ErrApplicationStandardRuntimeStale
		}
		return cloneRegistryRootfs(old), nil
	}
	value := DeploymentRegistryRootfs{ID: in.ID, Input: in, InputHash: hash, PublishedAt: now, ExpiresAt: parent.ExpiresAt}
	if m.deploymentRegistryRootfs == nil {
		m.deploymentRegistryRootfs = map[string]DeploymentRegistryRootfs{}
	}
	if m.deploymentRegistryRootfsCurrent == nil {
		m.deploymentRegistryRootfsCurrent = map[string]string{}
	}
	if in.WorkloadName == "" {
		dep.RootfsKey, dep.RootfsPath, dep.RootfsBytes = in.StorageKey, in.RootfsPath, in.ContentBytes
		m.deployments[dep.ID] = dep
	} else {
		key := dep.ID + "\x00" + in.WorkloadName
		layer := m.deploymentSidecarLayers[key]
		if layer.CreatedAt.IsZero() {
			layer.CreatedAt = now
		}
		layer.DeploymentID, layer.SidecarName, layer.StorageKey, layer.Bytes, layer.ContentDigest, layer.UpdatedAt = dep.ID, in.WorkloadName, in.StorageKey, in.ContentBytes, parent.Input.SelectedReference, now
		m.deploymentSidecarLayers[key] = layer
	}
	m.deploymentRegistryRootfs[in.ID] = value
	m.deploymentRegistryRootfsCurrent[pointer] = in.ID
	return cloneRegistryRootfs(value), nil
}

func (m *MemStore) GetCurrentDeploymentRegistryRootfs(ctx context.Context, accountID, appID, depID, workload string) (DeploymentRegistryRootfs, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, depID) {
		return DeploymentRegistryRootfs{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	app, found, dep, exists := m.registryVerificationOwnerLocked(appID, depID)
	if !found || !exists || app.Status == AppDeleted || !sameStandardUUID(app.AccountID, accountID) || !sameStandardUUID(dep.AppID, appID) {
		return DeploymentRegistryRootfs{}, ErrNotFound
	}
	id := m.deploymentRegistryRootfsCurrent[canonicalStandardUUID(depID)+"\x00"+workload]
	value, ok := m.deploymentRegistryRootfs[id]
	if !ok {
		return DeploymentRegistryRootfs{}, ErrNotFound
	}
	parent, ok := m.deploymentRegistryVerifications[value.Input.RegistryVerificationID]
	if !ok || !sameStandardUUID(parent.Input.AccountID, app.AccountID) || value.Input.AccountID != parent.Input.AccountID ||
		value.Input.OrgID != registryCanonicalOrg(app.OrgID) || !registryRootfsMatchesMetadata(value, dep, m.deploymentSidecarLayers[dep.ID+"\x00"+workload], parent) {
		return DeploymentRegistryRootfs{}, ErrNotFound
	}
	ref, err := registryWorkloadReference(dep, workload)
	if err != nil || ref != parent.Input.ImageReference {
		return DeploymentRegistryRootfs{}, ErrNotFound
	}
	if err := validateRegistryRootfsStored(value); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	return cloneRegistryRootfs(value), nil
}
