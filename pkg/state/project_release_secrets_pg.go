package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func loadReleaseSecretVersionsDB(ctx context.Context, db sqlc.DBTX, capture *projectReleaseConfigCapture, account string, request json.RawMessage) error {
	capture.SecretVersions = map[string]releaseSecretVersion{}
	ids := releaseSecretRequestIDs(request)
	if len(ids) == 0 {
		return nil
	}
	rows, err := sqlc.New().FindProjectReleaseSecretVersions(ctx, db, sqlc.FindProjectReleaseSecretVersionsParams{AccountID: mustPgUUID(account), ProjectID: mustPgUUID(capture.ProjectID), Since: pgtype.Timestamptz{Time: time.Now().Add(-api.ProjectReleaseSecretStageTTL), Valid: true}, Ids: ids})
	if err != nil {
		return err
	}
	for _, raw := range rows {
		var row releaseSecretVersion
		if json.Unmarshal(raw, &row) != nil {
			return ErrConflict
		}
		capture.SecretVersions[row.ID] = row
	}
	return nil
}
func (s *PgStore) StageProjectReleaseSecrets(ctx context.Context, account, project, request, hash string, rows []ProjectReleaseSealedSecret, workloads map[string]map[string]string) (api.ProjectReleaseSecretStageResponse, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.ProjectReleaseSecretStageResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockSafeProjectReleaseProject(ctx, tx, sqlc.LockSafeProjectReleaseProjectParams{AccountID: account, ProjectID: project}); err != nil {
		return api.ProjectReleaseSecretStageResponse{}, mapErr(err)
	}
	if err := q.LockSafeProjectReleaseApps(ctx, tx, project); err != nil {
		return api.ProjectReleaseSecretStageResponse{}, err
	}
	old, err := q.ReadProjectReleaseSecretBundle(ctx, tx, sqlc.ReadProjectReleaseSecretBundleParams{AccountID: mustPgUUID(account), RequestID: request})
	if err == nil {
		if pgUUIDString(old.ProjectID) != project || old.RequestHash != hash {
			return api.ProjectReleaseSecretStageResponse{}, ErrConflict
		}
		var response api.ProjectReleaseSecretStageResponse
		if json.Unmarshal(old.ResponseJson, &response) != nil {
			return response, ErrConflict
		}
		return response, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.ProjectReleaseSecretStageResponse{}, err
	}
	if err := q.PruneExpiredProjectReleaseSecretBundles(ctx, tx, sqlc.PruneExpiredProjectReleaseSecretBundlesParams{ProjectID: mustPgUUID(project), Before: pgtype.Timestamptz{Time: time.Now().Add(-api.ProjectReleaseSecretStageTTL), Valid: true}}); err != nil {
		return api.ProjectReleaseSecretStageResponse{}, err
	}
	count, err := q.CountActiveProjectReleaseSecretBundles(ctx, tx, sqlc.CountActiveProjectReleaseSecretBundlesParams{ProjectID: mustPgUUID(project), Since: pgtype.Timestamptz{Time: time.Now().Add(-api.ProjectReleaseSecretStageTTL), Valid: true}})
	if err != nil {
		return api.ProjectReleaseSecretStageResponse{}, err
	}
	if count >= api.ProjectReleaseSecretStagesPerProject {
		return api.ProjectReleaseSecretStageResponse{}, ErrConflict
	}
	bundle, err := newReleaseSecretBundle(account, project, request, hash, rows, workloads)
	if err != nil {
		return api.ProjectReleaseSecretStageResponse{}, err
	}
	for _, row := range bundle.Values {
		owner, err := q.ReleaseConfigurationCandidateOwner(ctx, tx, row.AppID)
		if err != nil || owner.AccountID != account || owner.ProjectID != project {
			return api.ProjectReleaseSecretStageResponse{}, ErrConflict
		}
	}
	values, _ := json.Marshal(bundle.Values)
	response, _ := json.Marshal(bundle.Response)
	if count, err := q.InsertProjectReleaseSecretBundle(ctx, tx, sqlc.InsertProjectReleaseSecretBundleParams{AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project), RequestID: request, RequestHash: hash, ValuesJson: values, ResponseJson: response}); err != nil {
		return api.ProjectReleaseSecretStageResponse{}, err
	} else if count != 1 {
		return api.ProjectReleaseSecretStageResponse{}, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return api.ProjectReleaseSecretStageResponse{}, err
	}
	return bundle.Response, nil
}
func (s *PgStore) ReleaseSecretsPinned(ctx context.Context, account, appID, scope string) (bool, error) {
	return sqlc.New().AppReleaseSecretsPinned(ctx, s.pool, sqlc.AppReleaseSecretsPinnedParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(appID), Scope: normalizedDeploymentScope(scope)})
}
func pinReleaseSecretsDB(ctx context.Context, db sqlc.DBTX, deployment string, rows []releaseSecretVersion) error {
	raw, err := json.Marshal(cloneReleaseSecrets(rows))
	if err != nil {
		return err
	}
	return sqlc.New().PinDeploymentReleaseSecrets(ctx, db, sqlc.PinDeploymentReleaseSecretsParams{DeploymentID: mustPgUUID(deployment), ValuesJson: raw})
}
func candidateReleaseSecretsDB(ctx context.Context, db sqlc.DBTX, d Deployment) ([]releaseSecretVersion, bool, error) {
	if d.releaseConfigAttempt == "" {
		owner, err := sqlc.New().ReleaseConfigurationCandidateOwner(ctx, db, d.AppID)
		if errors.Is(err, pgx.ErrNoRows) {
			// createDeployment already locked an active parent app. The owner
			// query additionally requires project membership; standalone apps
			// have no project release secret snapshot to inherit.
			return nil, false, nil
		}
		if err != nil {
			return nil, false, mapErr(err)
		}
		raw, err := sqlc.New().InheritedDeploymentReleaseSecrets(ctx, db, sqlc.InheritedDeploymentReleaseSecretsParams{AccountID: mustPgUUID(owner.AccountID), AppID: mustPgUUID(d.AppID), Scope: normalizedDeploymentScope(d.Scope)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		var rows []releaseSecretVersion
		if json.Unmarshal(raw, &rows) != nil {
			return nil, false, ErrConflict
		}
		return rows, true, nil
	}
	owner, err := sqlc.New().ReleaseConfigurationCandidateOwner(ctx, db, d.AppID)
	if err != nil {
		return nil, false, mapErr(err)
	}
	raw, err := sqlc.New().ReadStagedProjectReleaseConfiguration(ctx, db, sqlc.ReadStagedProjectReleaseConfigurationParams{AccountID: owner.AccountID, AttemptID: d.releaseConfigAttempt})
	if err != nil {
		return nil, false, mapErr(err)
	}
	var capture projectReleaseConfigCapture
	if json.Unmarshal(raw, &capture) != nil {
		return nil, false, ErrConflict
	}
	if capture.Descriptor == nil || capture.Descriptor.Secrets == nil {
		return nil, false, nil
	}
	for _, workload := range capture.Workloads {
		if workload.AppID == d.AppID {
			return cloneReleaseSecrets(workload.SecretTarget), true, nil
		}
	}
	return nil, false, ErrConflict
}
