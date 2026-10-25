package state

import "context"

// RollbackPreparedStore reports whether an explicit rollback prepared a
// deployment for activation (ADR-1446). imaged promotes such a target without
// the latest-revision fence: an older revision is the point of a rollback.
// PrepareDeploymentRollback sets the marker and promotion clears it.
type RollbackPreparedStore interface {
	DeploymentRollbackPrepared(ctx context.Context, id string) (bool, error)
}

type preparedRollbackPromotionKey struct{}

// WithPreparedRollbackPromotion marks a promotion as the activation of a
// prepared plain rollback. imaged reads the marker before it promotes; the
// store rechecks it under the deployment lock, so a rollback withdrawn in
// between (its receipt failed, for example rollback_timeout) is refused with
// ErrDeploymentSuperseded instead of cutting over after the failure was
// reported.
func WithPreparedRollbackPromotion(ctx context.Context) context.Context {
	return context.WithValue(ctx, preparedRollbackPromotionKey{}, true)
}

func preparedRollbackPromotion(ctx context.Context) bool {
	marked, _ := ctx.Value(preparedRollbackPromotionKey{}).(bool)
	return marked
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

// rollbackOutpacedLocked reports whether d is a prepared plain rollback whose
// scope saw another release stop serving after the request (see
// withdrawOutpacedRollbackTx).
func (m *MemStore) rollbackOutpacedLocked(d Deployment) bool {
	preparedAt, prepared := m.rollbackPrepared[d.ID]
	if !prepared {
		return false
	}
	for otherID, endedAt := range m.deploymentServingEndedAt {
		other, ok := m.deployments[otherID]
		if !ok || otherID == d.ID || other.AppID != d.AppID ||
			normalizedDeploymentScope(other.Scope) != normalizedDeploymentScope(d.Scope) {
			continue
		}
		if endedAt.After(preparedAt) {
			return true
		}
	}
	return false
}
