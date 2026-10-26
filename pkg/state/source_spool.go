package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SourceSpoolStore lists the build-spool files that a deployment still in
// flight may read (its source archive) or append to (its build log). The
// spool sweeper keeps them whatever their age (ADR-20261026152731).
type SourceSpoolStore interface {
	ListInFlightDeploymentSpoolPaths(ctx context.Context) ([]string, error)
}

var (
	_ SourceSpoolStore = (*PgStore)(nil)
	_ SourceSpoolStore = (*MemStore)(nil)
)

// deploymentInFlight reports whether a deployment can still build or image.
func deploymentInFlight(status DeploymentStatus) bool {
	switch status {
	case DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting:
		return true
	default:
		return false
	}
}

func (s *PgStore) ListInFlightDeploymentSpoolPaths(ctx context.Context) ([]string, error) {
	rows, err := sqlc.New().ListInFlightDeploymentSpoolPaths(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("state: list in-flight spool paths: %w", err)
	}
	out := make([]string, 0, 2*len(rows))
	for _, row := range rows {
		out = appendSpoolPaths(out, row.SourcePath, row.LogPath)
	}
	return out, nil
}

func (m *MemStore) ListInFlightDeploymentSpoolPaths(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, d := range m.deployments {
		if deploymentInFlight(d.Status) {
			out = appendSpoolPaths(out, d.SourcePath, d.LogPath)
		}
	}
	return out, nil
}

func appendSpoolPaths(out []string, paths ...string) []string {
	for _, p := range paths {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
