package state

import (
	"context"
	"time"
)

var _ SourceImagePreparationStore = (*MemStore)(nil)

func (m *MemStore) PublishSourceImagePreparationLayer(ctx context.Context, input SourceBuildRootfsInput, token string) (SourceBuildRootfs, error) {
	in, hash, err := prepareSourceBuildRootfs(input)
	if err != nil {
		return SourceBuildRootfs{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SourceBuildRootfs{}, err
	}
	_, found, dep, exists := m.registryVerificationOwnerLocked(in.AppID, in.DeploymentID)
	if !found || !exists {
		return SourceBuildRootfs{}, ErrNotFound
	}
	p, exists := m.imagePreparations[dep.ID]
	if !exists || p.ClaimToken != token || p.Phase != ImagePreparing || dep.Status != DeployImaging {
		return SourceBuildRootfs{}, ErrConflict
	}
	value, err := m.publishSourceBuildRootfsLocked(in, hash)
	if err != nil {
		return SourceBuildRootfs{}, err
	}
	p.Phase, p.UpdatedAt = ImageLayerPublished, time.Now().UTC()
	m.imagePreparations[dep.ID] = p
	return value, nil
}
