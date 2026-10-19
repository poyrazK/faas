package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func releaseStageFromSQL(row sqlc.ProjectReleaseStage) (projectReleaseStage, error) {
	stage := projectReleaseStage{ID: uuid.UUID(row.ID.Bytes).String(), AccountID: uuid.UUID(row.AccountID.Bytes).String(), ProjectID: uuid.UUID(row.ProjectID.Bytes).String(), CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time, Cancelled: row.Cancelled}
	if err := json.Unmarshal(row.Manifest, &stage.Manifest); err != nil {
		return stage, err
	}
	return stage, json.Unmarshal(row.Targets, &stage.Targets)
}
func releaseStageSnapshotPG(ctx context.Context, db sqlc.DBTX, account, project, scope, source string) (releaseStageSnapshot, error) {
	var out releaseStageSnapshot
	raw, err := sqlc.New().ProjectReleaseStageSnapshot(ctx, db, sqlc.ProjectReleaseStageSnapshotParams{AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project), Scope: scope})
	if err != nil {
		return out, mapErr(err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	if source != "" {
		raw, err := sqlc.New().CheckedRollbackSourceForShare(ctx, db, sqlc.CheckedRollbackSourceForShareParams{ID: mustPgUUID(source), AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project), Environment: scope})
		if errors.Is(err, pgx.ErrNoRows) {
			return out, nil
		}
		if err != nil {
			return out, mapErr(err)
		}
		release, err := decodeProjectReleaseSet(raw)
		if err != nil {
			return out, err
		}
		out.Source = &release
	}
	return out, nil
}
func (s *PgStore) StageProjectRelease(ctx context.Context, account, project string, request api.StageProjectReleaseRequest) (api.ProjectReleaseStage, error) {
	if request.TTLSeconds < 1 || request.TTLSeconds > api.ProjectReleaseStageMaxTTLSeconds {
		return api.ProjectReleaseStage{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.ProjectReleaseStage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stage, snapshot, err := stageProjectReleasePG(ctx, tx, account, project, request)
	if err != nil {
		return api.ProjectReleaseStage{}, checkedGraphWriteError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return api.ProjectReleaseStage{}, checkedGraphWriteError(err)
	}
	return s.releaseStageViewPG(ctx, stage, snapshot)
}
func stageProjectReleasePG(ctx context.Context, tx pgx.Tx, account, project string, request api.StageProjectReleaseRequest) (projectReleaseStage, releaseStageSnapshot, error) {
	var stage projectReleaseStage
	var snapshot releaseStageSnapshot
	q := sqlc.New()
	if _, err := q.LockProjectEnvironmentCloneProject(ctx, tx, sqlc.LockProjectEnvironmentCloneProjectParams{AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project)}); err != nil {
		return stage, snapshot, mapErr(err)
	}
	ids, err := q.LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project)})
	if err != nil {
		return stage, snapshot, err
	}
	appIDs := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		appIDs = append(appIDs, mustPgUUID(id))
	}
	if _, err := q.LockProjectReleaseStageDeployments(ctx, tx, appIDs); err != nil {
		return stage, snapshot, err
	}
	if _, err := q.LockProjectReleaseStagePolicies(ctx, tx, sqlc.LockProjectReleaseStagePoliciesParams{AppIds: appIDs, Scope: request.Manifest.Environment}); err != nil {
		return stage, snapshot, err
	}
	snapshot, err = releaseStageSnapshotPG(ctx, tx, account, project, request.Manifest.Environment, request.Manifest.RollbackReleaseID)
	if err != nil {
		return stage, snapshot, err
	}
	targets, err := validateReleaseStage(snapshot, request.Manifest)
	if err != nil {
		return stage, snapshot, err
	}
	for _, app := range snapshot.Apps {
		id := request.Manifest.Deployments[app.Slug]
		if err := requireDeploymentLayerArtifactsTx(ctx, tx, id); err != nil {
			return stage, snapshot, err
		}
	}
	rows, err := q.ProjectReleaseStages(ctx, tx, sqlc.ProjectReleaseStagesParams{AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project), Environment: request.Manifest.Environment})
	if err != nil {
		return stage, snapshot, err
	}
	encoded, err := json.Marshal(request.Manifest)
	if err != nil {
		return stage, snapshot, err
	}
	for _, row := range rows {
		if !row.Cancelled && time.Now().Before(row.ExpiresAt.Time) {
			old, err := releaseStageFromSQL(row)
			if err != nil {
				return stage, snapshot, err
			}
			previous, _ := json.Marshal(old.Manifest)
			if string(encoded) != string(previous) {
				return stage, snapshot, ErrConflict
			}
			return old, snapshot, nil
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	stage = projectReleaseStage{ID: uuid.NewString(), AccountID: account, ProjectID: project, Manifest: request.Manifest, Targets: targets, CreatedAt: now, ExpiresAt: now.Add(time.Duration(request.TTLSeconds) * time.Second)}
	targetJSON, err := json.Marshal(targets)
	if err != nil {
		return stage, snapshot, err
	}
	err = q.InsertProjectReleaseStage(ctx, tx, sqlc.InsertProjectReleaseStageParams{ID: mustPgUUID(stage.ID), AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project), Environment: request.Manifest.Environment, Manifest: encoded, Targets: targetJSON, CreatedAt: serviceRecoveryTime(now), ExpiresAt: serviceRecoveryTime(stage.ExpiresAt)})
	return stage, snapshot, err
}
func (s *PgStore) releaseStageViewPG(ctx context.Context, stage projectReleaseStage, snapshot releaseStageSnapshot) (api.ProjectReleaseStage, error) {
	instances := []Instance{}
	recovery := map[string]ServiceRecovery{}
	for _, app := range snapshot.Apps {
		rows, err := s.ListInstancesForApp(ctx, app.ID)
		if err != nil {
			return api.ProjectReleaseStage{}, err
		}
		instances = append(instances, rows...)
		row, err := s.ServiceRecoveryByApp(ctx, app.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return api.ProjectReleaseStage{}, err
		}
		if err == nil {
			recovery[app.ID] = row
		}
	}
	return releaseStageView(stage, snapshot, instances, recovery), nil
}
func (s *PgStore) ProjectReleaseStage(ctx context.Context, account, project, scope, id string) (api.ProjectReleaseStage, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		return api.ProjectReleaseStage{}, ErrNotFound
	}
	row, err := sqlc.New().ProjectReleaseStageByID(ctx, s.pool, sqlc.ProjectReleaseStageByIDParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project), Environment: scope})
	if err != nil {
		return api.ProjectReleaseStage{}, mapErr(err)
	}
	stage, err := releaseStageFromSQL(row)
	if err != nil {
		return api.ProjectReleaseStage{}, err
	}
	snapshot, err := releaseStageSnapshotPG(ctx, s.pool, account, project, scope, stage.Manifest.RollbackReleaseID)
	if err != nil {
		return api.ProjectReleaseStage{}, err
	}
	return s.releaseStageViewPG(ctx, stage, snapshot)
}
func (s *PgStore) CancelProjectReleaseStage(ctx context.Context, account, project, scope, id string) (api.ProjectReleaseStage, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		return api.ProjectReleaseStage{}, ErrNotFound
	}
	n, err := sqlc.New().CancelProjectReleaseStage(ctx, s.pool, sqlc.CancelProjectReleaseStageParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project), Environment: scope})
	if err != nil {
		return api.ProjectReleaseStage{}, err
	}
	if n == 0 {
		return api.ProjectReleaseStage{}, ErrNotFound
	}
	return s.ProjectReleaseStage(ctx, account, project, scope, id)
}
func (s *PgStore) ServiceReleaseReadinessTargets(ctx context.Context, appID string) (map[string]int, error) {
	out := map[string]int{}
	app, err := s.AppByID(ctx, appID)
	if err != nil {
		return out, err
	}
	rows, err := sqlc.New().ServiceProjectReleaseStages(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		stage, err := releaseStageFromSQL(row)
		if err != nil {
			return out, err
		}
		snapshot, err := releaseStageSnapshotPG(ctx, s.pool, stage.AccountID, stage.ProjectID, stage.Manifest.Environment, stage.Manifest.RollbackReleaseID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return out, err
		}
		targets, err := validateReleaseStage(snapshot, stage.Manifest)
		if err != nil {
			continue
		}
		for id, n := range targets {
			if snapshot.Deployments[id].AppID == appID && n > out[id] {
				out[id] = n
			}
		}
	}
	ids, err := sqlc.New().ActiveServiceProjectReleaseTargets(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return out, err
	}
	desired := 1
	if app.Manifest.ServiceReplicas != nil && app.Manifest.ServiceReplicas.Desired > desired {
		desired = app.Manifest.ServiceReplicas.Desired
	}
	for _, id := range ids {
		out[id] = desired
	}
	return out, nil
}
