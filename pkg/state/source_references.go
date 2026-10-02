package state

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// BuildSourceKey preserves the committed reference. Older deployments have no
// reference and retain their original, unprefixed build-ID object spelling.
func BuildSourceKey(dep Deployment, buildID string) string {
	if dep.SourceStorageKey != "" {
		return dep.SourceStorageKey
	}
	return "sources/" + buildID + ".tar.gz"
}

func validSourceReference(computeRegion, key string) bool {
	if key == "" {
		return true
	}
	id, relative, err := region.SplitArtifactKey(key)
	if err != nil || id != computeRegion || !strings.HasPrefix(relative, "sources/") || !strings.HasSuffix(relative, ".tar.gz") {
		return false
	}
	buildID := strings.TrimSuffix(strings.TrimPrefix(relative, "sources/"), ".tar.gz")
	u, err := uuid.Parse(buildID)
	return err == nil && (buildID == u.String() || buildID == strings.ReplaceAll(u.String(), "-", ""))
}

// SourceArtifactReferenceStore protects retained source objects shared with a
// pending retry. The object's original build may already be terminal.
type SourceArtifactReferenceStore interface {
	SourceArtifactReferencedByPendingBuild(context.Context, string) (bool, error)
}

// SourceRetryStore binds an already-published new source object to the retry in
// the same transaction that creates its immutable deployment inputs.
type SourceRetryStore interface {
	RetryDeploymentFromStageWithSource(context.Context, string, StageName, string) (Deployment, error)
}

func (s *PgStore) SourceArtifactReferencedByPendingBuild(ctx context.Context, key string) (bool, error) {
	return sqlc.New().SourceArtifactReferencedByPendingBuild(ctx, s.pool, key)
}

func (m *MemStore) SourceArtifactReferencedByPendingBuild(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, dep := range m.deployments {
		if dep.SourceStorageKey != key || dep.Status != DeployPending && dep.Status != DeployBuilding {
			continue
		}
		hasBuild := false
		for _, build := range m.builds {
			if build.DeploymentID != dep.ID {
				continue
			}
			hasBuild = true
			if build.Status == BuildQueued || build.Status == BuildRunning {
				return true, nil
			}
		}
		if !hasBuild {
			return true, nil
		}
	}
	return false, nil
}
