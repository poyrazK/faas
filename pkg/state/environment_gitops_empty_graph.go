package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsEmptyGraphGuard = (*PgStore)(nil)
var _ EnvironmentGitOpsEmptyGraphGuard = (*MemStore)(nil)

func (s *PgStore) EnvironmentGitOpsHasServingManagedWorkload(ctx context.Context, lease EnvironmentGitOpsLease) (bool, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	managed, err := sqlc.New().EnvironmentGitOpsHasServingManagedWorkload(ctx, tx,
		sqlc.EnvironmentGitOpsHasServingManagedWorkloadParams{AccountID: mustPgUUID(lease.Source.AccountID),
			ProjectID: mustPgUUID(lease.Source.ProjectID), Scope: lease.Source.EnvironmentSlug})
	return managed, mapErr(err)
}

func (m *MemStore) EnvironmentGitOpsHasServingManagedWorkload(_ context.Context, lease EnvironmentGitOpsLease) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return false, err
	}
	id := m.activeProjectReleaseSets[releaseKey(memory.source.ProjectID, memory.source.EnvironmentSlug)]
	if id != "" {
		release, ok := m.projectReleaseSets[id]
		if !ok || !release.Active || release.AccountID != memory.source.AccountID || release.ProjectID != memory.source.ProjectID ||
			normalizedDeploymentScope(release.EnvironmentSlug) != normalizedDeploymentScope(memory.source.EnvironmentSlug) {
			return false, ErrConflict
		}
		if m.releaseSetHasManagedMembersLocked(release) {
			return true, nil
		}
	}
	for _, deployment := range m.deployments {
		app := m.apps[deployment.AppID]
		if app.AccountID == memory.source.AccountID && app.ProjectID == memory.source.ProjectID &&
			normalizedDeploymentScope(deployment.Scope) == normalizedDeploymentScope(memory.source.EnvironmentSlug) &&
			deployment.Status == DeployLive && !deployment.EnvironmentWorkloadHeld() && deployment.EnvironmentWorkloadManaged() {
			return true, nil
		}
	}
	return false, nil
}
