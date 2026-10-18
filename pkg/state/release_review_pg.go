package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ReadReleaseReview(ctx context.Context, appID, scope string) (api.AppReleaseReview, error) {
	return readReleaseReview(ctx, s.pool, appID, normalizedDeploymentScope(scope))
}

func readReleaseReview(ctx context.Context, db sqlc.DBTX, appID, scope string) (api.AppReleaseReview, error) {
	row, err := sqlc.New().ReadReleaseReview(ctx, db, sqlc.ReadReleaseReviewParams{AppID: mustPgUUID(appID), Scope: scope})
	if err != nil {
		return api.AppReleaseReview{}, mapErr(err)
	}
	return api.AppReleaseReview{AppID: row.AppID, Scope: row.Scope, ProjectID: row.ProjectID, EnvironmentID: row.EnvironmentID, WorkloadRevision: row.WorkloadRevision, WorkloadConfigHash: row.WorkloadConfigHash, Protected: row.Protected, ConfigurationHash: row.ConfigurationHash, LatestDeploymentID: row.LatestDeploymentID, ServingDeploymentIDs: row.ServingDeploymentIds}, nil
}
