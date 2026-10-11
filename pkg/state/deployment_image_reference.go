package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// DeploymentImageReferenceStore pins mutable project image intent before
// materialization. The compare-and-set fences stale workers; retries inherit
// this immutable source, including a signed multi-platform index.
type DeploymentImageReferenceStore interface {
	PinDeploymentImageReference(ctx context.Context, id, expected, pinned string) error
}

func (m *MemStore) PinDeploymentImageReference(_ context.Context, id, expected, pinned string) error {
	if !api.ValidDeploymentImage(pinned) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	if d.Kind != DeploymentKindImage || d.ImageDigest != expected ||
		(d.Status != DeployPending && d.Status != DeployBuilding && d.Status != DeployImaging) {
		return ErrInvalidStateTransition
	}
	d.ImageDigest = pinned
	m.putDeploymentLocked(id, d)
	return nil
}

func (s *PgStore) PinDeploymentImageReference(ctx context.Context, id, expected, pinned string) error {
	if !api.ValidDeploymentImage(pinned) {
		return ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `update deployments set image_digest = $3
		where id = $1 and image_digest = $2 and kind = 'image'
		and status in ('pending', 'building', 'imaging')`, id, expected, pinned)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var status DeploymentStatus
	if err := s.pool.QueryRow(ctx, `select status from deployments where id = $1`, id).Scan(&status); err != nil {
		return mapErr(err)
	}
	return ErrInvalidStateTransition
}

var _ DeploymentImageReferenceStore = (*MemStore)(nil)
var _ DeploymentImageReferenceStore = (*PgStore)(nil)
