package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// The caller holds the app lock used by release-task starts.
func checkUnreviewedRollbackTx(ctx context.Context, tx pgx.Tx, app, scope, target string) error {
	if target == "" {
		return nil
	}
	traffic, err := sqlc.New().ServiceRolloutRecipientTraffic(ctx, tx, mustPgUUID(target))
	if err != nil {
		return err
	}
	if traffic == 100 {
		return nil // Finishing an abort cannot restore code that is already serving.
	}
	runs, err := releaseRunsCrossedByDBTX(ctx, tx, app, target)
	if err != nil {
		return err
	}
	return checkRollbackRunsTx(ctx, tx, app, scope, target, runs)
}

func checkRollbackRunsTx(ctx context.Context, tx pgx.Tx, app, scope, target string, runs []ReleaseRun) error {
	undone, err := sqlc.New().ListReleasesUndoneByDatabaseSwitches(ctx, tx, sqlc.ListReleasesUndoneByDatabaseSwitchesParams{AppID: mustPgUUID(app), Scope: normalizedDeploymentScope(scope)})
	if err != nil {
		return err
	}
	return checkRollbackReleaseReview(target, runs, undone, nil)
}

func (m *MemStore) checkUnreviewedRollbackLocked(app, scope, target string) error {
	if target == "" || m.deployments[target].TrafficPercent == 100 {
		return nil
	}
	return checkRollbackReleaseReview(target, m.releaseRunsCrossedByLocked(app, target), m.releasesUndoneByDatabaseSwitchesLocked(app, scope), nil)
}

func (m *MemStore) releaseRunsAfterLocked(app, scope, target string, since time.Time) []ReleaseRun {
	var runs []ReleaseRun
	for _, task := range m.appTasks {
		if task.AppID == app && task.Kind == AppTaskKindRelease && task.StartedAt != nil && task.StartedAt.After(since) && task.DeploymentID != target && releaseScope(task.DeploymentScope) == releaseScope(scope) {
			runs = append(runs, ReleaseRun{DeploymentID: task.DeploymentID, StartedAt: *task.StartedAt})
		}
	}
	return runs
}
