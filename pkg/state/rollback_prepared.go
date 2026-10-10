package state

import "context"

// RollbackPreparedStore reports whether an explicit rollback prepared a
// deployment for activation (ADR-911). imaged promotes such a target without
// the latest-revision fence: an older revision is the point of a rollback.
// PrepareDeploymentRollback sets the marker and promotion clears it.
type RollbackPreparedStore interface {
	DeploymentRollbackPrepared(ctx context.Context, id string) (bool, error)
}

func (s *PgStore) DeploymentRollbackPrepared(ctx context.Context, id string) (bool, error) {
	var prepared bool
	if err := s.pool.QueryRow(ctx, `select rollback_prepared_at is not null from deployments where id = $1`, id).Scan(&prepared); err != nil {
		return false, mapErr(err)
	}
	return prepared, nil
}

func (m *MemStore) DeploymentRollbackPrepared(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.deployments[id]; !ok {
		return false, ErrNotFound
	}
	_, prepared := m.rollbackPrepared[id]
	return prepared, nil
}
