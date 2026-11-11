package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Deployment creation and activation serialize with stage deletion, including
// callers whose HTTP preflight ran before the environment was removed.
func (m *MemStore) checkProjectDeploymentEnvironmentLocked(app App, scope string) error {
	if app.ProjectID == "" || IsProductionScope(scope) {
		return nil
	}
	_, err := m.projectEnvironmentBySlugLocked(app.ProjectID, scope)
	return err
}

func lockProjectDeploymentEnvironment(ctx context.Context, db sqlc.DBTX, appID, scope string) error {
	if IsProductionScope(scope) {
		return nil
	}
	hasProject, err := sqlc.New().DeploymentAppHasProject(ctx, db, mustPgUUID(appID))
	if err != nil {
		return mapErr(err)
	}
	if !hasProject {
		return nil
	}
	_, err = sqlc.New().LockProjectDeploymentEnvironment(ctx, db, sqlc.LockProjectDeploymentEnvironmentParams{
		AppID: mustPgUUID(appID), Scope: scope,
	})
	return mapErr(err)
}
