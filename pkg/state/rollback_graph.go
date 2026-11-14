package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrProjectReleaseRollbackRequired = errors.New("state: rollback requires project release recovery")

func rollbackGraphScope(scope string) string {
	if IsProductionScope(scope) {
		return "production"
	}
	return normalizedDeploymentScope(scope)
}

// Graph publication takes these same app locks. Inspect its current selection
// without taking a project lock in the reverse order.
func (m *MemStore) checkAppRollbackGraphLocked(app, scope, target string) error {
	a := m.apps[app]
	if a.ProjectID == "" || a.PreviewOfSlug != "" {
		return nil
	}
	if id := m.activeProjectReleaseSets[releaseKey(a.ProjectID, rollbackGraphScope(scope))]; id != "" {
		release := m.projectReleaseSets[id]
		if release.AccountID == a.AccountID && releaseMemberForApp(release, app) != target {
			return ErrProjectReleaseRollbackRequired
		}
	}
	return nil
}

func checkAppRollbackGraphTx(ctx context.Context, tx pgx.Tx, app, scope, target string) error {
	q := sqlc.New()
	if err := q.GuardWorkflowAdmissionSnapshot(ctx, tx, app); err != nil {
		return err
	}
	blocked, err := q.ActiveGraphBlocksAppRollback(ctx, tx, sqlc.ActiveGraphBlocksAppRollbackParams{AppID: mustPgUUID(app), Scope: rollbackGraphScope(scope), TargetID: mustPgUUID(target)})
	if err != nil {
		return err
	}
	if blocked {
		return ErrProjectReleaseRollbackRequired
	}
	return nil
}
