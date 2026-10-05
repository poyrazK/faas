package state

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// BuildArtifactReference survives replacement of RootfsPath/Key by the final
// runtime layer. It is committed together with build success and provenance.
type BuildArtifactReference struct {
	Key    string
	SHA256 string
	Bytes  int64
}

func (r BuildArtifactReference) ValidFor(computeRegion string) bool {
	if r == (BuildArtifactReference{}) {
		return computeRegion == ""
	}
	id, _, digest, err := region.ParseBuildExportKey(r.Key)
	return computeRegion != "" && err == nil && id == computeRegion && digest == r.SHA256 && r.Bytes > 0
}

func (d Deployment) BuildArtifactReference() BuildArtifactReference {
	return BuildArtifactReference{Key: d.BuildArtifactKey, SHA256: d.BuildArtifactSHA256, Bytes: d.BuildArtifactBytes}
}

type BuildArtifactCompletionStore interface {
	CompleteBuildWithArtifact(context.Context, Build, string, string, int64, BuildProvenance, BuildArtifactReference) error
}

type BuildArtifactReferenceStore interface {
	BuildArtifactReferencedForMaterialization(context.Context, string) (bool, error)
}

type BuildImageMaterializationLocker interface {
	AcquireBuildImageMaterializationLock(context.Context, string) (func(context.Context), error)
}

func (s *PgStore) AcquireBuildImageMaterializationLock(ctx context.Context, id string) (func(context.Context), error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrInvalidArgument
	}
	return s.AcquireDeploymentActivationLock(ctx, id)
}

func (m *MemStore) AcquireBuildImageMaterializationLock(ctx context.Context, id string) (func(context.Context), error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrInvalidArgument
	}
	return m.AcquireDeploymentActivationLock(ctx, id)
}

func (s *PgStore) BuildArtifactReferencedForMaterialization(ctx context.Context, key string) (bool, error) {
	return sqlc.New().BuildArtifactReferencedForMaterialization(ctx, s.pool, key)
}

func (m *MemStore) BuildArtifactReferencedForMaterialization(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, dep := range m.deployments {
		if dep.BuildArtifactKey == key && buildImageStatusActive(dep.Status) {
			return true, nil
		}
	}
	return false, nil
}

func buildImageStatusActive(status DeploymentStatus) bool {
	return status == DeployPending || status == DeployBuilding || status == DeployImaging || status == DeploySnapshotting
}

type BuildImageStateStore interface {
	AdvanceBuildImageStatus(context.Context, string, DeploymentStatus, DeploymentStatus) error
}

func validBuildImageAdvance(from, to DeploymentStatus) bool {
	return (to == DeployImaging && (from == DeployPending || from == DeployBuilding || from == DeployImaging)) ||
		(to == DeploySnapshotting && from == DeployImaging)
}

func (s *PgStore) AdvanceBuildImageStatus(ctx context.Context, id string, from, to DeploymentStatus) error {
	if !validBuildImageAdvance(from, to) {
		return ErrInvalidArgument
	}
	depID, err := parsePgUUID(id)
	if err != nil {
		return err
	}
	n, err := sqlc.New().AdvanceBuildImageStatus(ctx, s.pool, sqlc.AdvanceBuildImageStatusParams{
		DeploymentID: depID, ExpectedStatus: string(from), NextStatus: string(to),
	})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrInvalidStateTransition
	}
	return nil
}

func (m *MemStore) AdvanceBuildImageStatus(_ context.Context, id string, from, to DeploymentStatus) error {
	if !validBuildImageAdvance(from, to) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dep, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	if dep.Status != from || dep.BuildArtifactKey == "" {
		return ErrInvalidStateTransition
	}
	dep.Status = to
	m.deployments[id] = dep
	return nil
}
