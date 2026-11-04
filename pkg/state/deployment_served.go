package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DeploymentServingHistoryStore answers whether a deployment ever took
// traffic. A superseded release that never served is a forward release, not
// a known-good rollback target (ADR-20261020085706 amendment 10).
type DeploymentServingHistoryStore interface {
	DeploymentServedBefore(ctx context.Context, deploymentID string) (bool, error)
}

// DeploymentServedBefore reports whether deploymentID serves now or stopped
// serving at some point (deployments.serving_ended_at). An unknown
// deployment is ErrNotFound.
func (s *PgStore) DeploymentServedBefore(ctx context.Context, deploymentID string) (bool, error) {
	id, err := parsePgUUID(deploymentID)
	if err != nil {
		return false, ErrNotFound
	}
	served, err := sqlc.New().DeploymentServedBefore(ctx, s.pool, id)
	if err != nil {
		return false, fmt.Errorf("state: deployment served before: %w", mapErr(err))
	}
	return served, nil
}

// DeploymentServedBefore mirrors PgStore.DeploymentServedBefore.
func (m *MemStore) DeploymentServedBefore(_ context.Context, deploymentID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[deploymentID]
	if !ok {
		return false, ErrNotFound
	}
	return deploymentServing(d) || m.deploymentServedLocked(deploymentID), nil
}
