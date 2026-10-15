package state

import (
	"context"
	"time"
)

var _ RegistryImagePreparationStore = (*MemStore)(nil)

func (m *MemStore) PublishRegistryImagePreparationLayer(ctx context.Context, input DeploymentRegistryRootfsInput, token string) (DeploymentRegistryRootfs, error) {
	in, hash, err := prepareRegistryRootfs(input)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	if in.WorkloadName != "" {
		return DeploymentRegistryRootfs{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	_, found, dep, exists := m.registryVerificationOwnerLocked(in.AppID, in.DeploymentID)
	if !found || !exists {
		return DeploymentRegistryRootfs{}, ErrNotFound
	}
	p, exists := m.imagePreparations[dep.ID]
	if !exists || p.ClaimToken != token || p.Phase != ImagePreparing || dep.Status != DeployImaging {
		return DeploymentRegistryRootfs{}, ErrConflict
	}
	value, err := m.publishDeploymentRegistryRootfsLocked(in, hash)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	p.Phase, p.UpdatedAt = ImageLayerPublished, time.Now().UTC()
	m.imagePreparations[dep.ID] = p
	return value, nil
}
