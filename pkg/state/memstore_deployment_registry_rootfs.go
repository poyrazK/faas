package state

// adr: 435

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
	return m.publishDeploymentRegistryRootfsLocked(in, hash)
}

func (m *MemStore) publishDeploymentRegistryRootfsLocked(in DeploymentRegistryRootfsInput, hash string) (DeploymentRegistryRootfs, error) {
	dep, parent, now, err := m.registryRootfsParentsLocked(in)
	if err != nil {
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
	return m.installRegistryRootfsLocked(in, hash, dep, parent, now)
}

func (m *MemStore) registryRootfsParentsLocked(in DeploymentRegistryRootfsInput) (Deployment, DeploymentRegistryVerification, time.Time, error) {
	app, found, dep, exists := m.registryVerificationOwnerLocked(in.AppID, in.DeploymentID)
	if !found || !exists {
		return Deployment{}, DeploymentRegistryVerification{}, time.Time{}, ErrNotFound
	}
	parent, ok := m.deploymentRegistryVerifications[in.RegistryVerificationID]
	if !ok {
		return Deployment{}, DeploymentRegistryVerification{}, time.Time{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := checkRegistryRootfsParent(in, parent, dep, now); err != nil {
		return Deployment{}, DeploymentRegistryVerification{}, time.Time{}, err
	}
	if err := checkRegistryVerificationOwner(parent.Input, app, dep); err != nil {
		return Deployment{}, DeploymentRegistryVerification{}, time.Time{}, err
	}
	key := m.currentPublisherKeyLocked(in.AccountID, app.ID, parent.Input.Proof.PublisherKeySHA256)
	if err := verifyRegistryCurrentKey(parent.Input, key); err != nil {
		return Deployment{}, DeploymentRegistryVerification{}, time.Time{}, err
	}
	if in.BaseProducerID != "" {
		base, ok := m.baseImageProducers[in.BaseProducerID]
		if !ok {
			return Deployment{}, DeploymentRegistryVerification{}, time.Time{}, ErrNotFound
		}
		if m.baseImageProducerCurrent[base.Input.Artifact.StorageKey] != base.ID {
			return Deployment{}, DeploymentRegistryVerification{}, time.Time{}, ErrApplicationStandardRuntimeStale
		}
		if err := checkRegistryRootfsBase(in, parent, base); err != nil {
			return Deployment{}, DeploymentRegistryVerification{}, time.Time{}, err
		}
		if err := checkRegistryRuntimeDefaultBase(in, base, app.Runtime); err != nil {
			return Deployment{}, DeploymentRegistryVerification{}, time.Time{}, err
		}
	}
	return dep, parent, now, nil
}

func (m *MemStore) installRegistryRootfsLocked(in DeploymentRegistryRootfsInput, hash string, dep Deployment, parent DeploymentRegistryVerification, now time.Time) (DeploymentRegistryRootfs, error) {
	pointer := in.DeploymentID + "\x00" + in.WorkloadName
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
