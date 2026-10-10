package state

import (
	"context"
	"encoding/hex"
	"strings"
)

// DeploymentBuildDigestStore pins the OCI manifest of a source build before
// imaged replaces its export path with the published application layer.
type DeploymentBuildDigestStore interface {
	PinDeploymentBuildDigest(context.Context, string, string, string) error
}

func validBuildDigest(export, digest string) bool {
	if export == "" || !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 || strings.ToLower(digest) != digest {
		return false
	}
	_, err := hex.DecodeString(digest[7:])
	return err == nil
}

func (m *MemStore) PinDeploymentBuildDigest(_ context.Context, id, export, digest string) error {
	if !validBuildDigest(export, digest) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	if d.Kind == DeploymentKindImage || d.RootfsPath != export ||
		(d.ImageDigest != "" && d.ImageDigest != digest) ||
		(d.Status != DeployPending && d.Status != DeployBuilding && d.Status != DeployImaging) {
		return ErrInvalidStateTransition
	}
	d.ImageDigest = digest
	m.putDeploymentLocked(id, d)
	return nil
}

func (s *PgStore) PinDeploymentBuildDigest(ctx context.Context, id, export, digest string) error {
	if !validBuildDigest(export, digest) {
		return ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `update deployments set image_digest=$3
		where id=$1 and rootfs_path=$2 and kind<>'image'
		and (image_digest='' or image_digest=$3)
		and status in ('pending','building','imaging')`, id, export, digest)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var status DeploymentStatus
	if err := s.pool.QueryRow(ctx, `select status from deployments where id=$1`, id).Scan(&status); err != nil {
		return mapErr(err)
	}
	return ErrInvalidStateTransition
}

var _ DeploymentBuildDigestStore = (*MemStore)(nil)
var _ DeploymentBuildDigestStore = (*PgStore)(nil)
