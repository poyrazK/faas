package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ADR-20261031193635 environment branches, PostgreSQL store.

const projectEnvironmentBranchSelect = `
	select b.environment_id::text, b.account_id::text, b.project_id::text, e.slug,
	       b.parent_environment_id::text, p.slug, b.branch_point, b.expires_at,
	       b.egress_mode, b.depth::int, b.created_at
	  from project_environment_branches b
	  join project_environments e on e.id = b.environment_id
	  join project_environments p on p.id = b.parent_environment_id`

func scanProjectEnvironmentBranch(row pgx.Row) (ProjectEnvironmentBranch, error) {
	var b ProjectEnvironmentBranch
	err := row.Scan(&b.EnvironmentID, &b.AccountID, &b.ProjectID, &b.Slug, &b.ParentEnvironmentID, &b.ParentSlug,
		&b.BranchPoint, &b.ExpiresAt, &b.EgressMode, &b.Depth, &b.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironmentBranch{}, ErrNotFound
	}
	if err != nil {
		return ProjectEnvironmentBranch{}, mapErr(err)
	}
	b.BranchPoint, b.ExpiresAt, b.CreatedAt = b.BranchPoint.UTC(), b.ExpiresAt.UTC(), b.CreatedAt.UTC()
	return b, nil
}

func (s *PgStore) CreateProjectEnvironmentBranch(ctx context.Context, req ProjectEnvironmentBranchCreate, limits api.Limits) (ProjectEnvironment, ProjectEnvironmentBranch, ProjectEnvironmentCloneResult, error) {
	if err := req.validate(); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentBranch{}, ProjectEnvironmentCloneResult{}, err
	}
	var branch ProjectEnvironmentBranch
	clone := ProjectEnvironmentClone{AccountID: req.AccountID, ProjectID: req.ProjectID, SourceSlug: req.ParentSlug, TargetSlug: req.Slug,
		branch: &req, branchOut: &branch}
	created, result, err := s.cloneProjectEnvironment(ctx, clone, limits, nil)
	if err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentBranch{}, ProjectEnvironmentCloneResult{}, err
	}
	return created, branch, result, nil
}

// insertProjectEnvironmentBranchTx records the branch inside the clone
// transaction, after its environment row exists.
func insertProjectEnvironmentBranchTx(ctx context.Context, tx pgx.Tx, req ProjectEnvironmentBranchCreate, created ProjectEnvironment) (ProjectEnvironmentBranch, error) {
	var parentID string
	var parentDepth int
	err := tx.QueryRow(ctx, `
		select e.id::text, coalesce(b.depth, 0)::int
		  from project_environments e
		  left join project_environment_branches b on b.environment_id = e.id
		 where e.account_id = $1 and e.project_id = $2 and e.slug = $3
	`, req.AccountID, req.ProjectID, req.ParentSlug).Scan(&parentID, &parentDepth)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironmentBranch{}, ErrNotFound
	}
	if err != nil {
		return ProjectEnvironmentBranch{}, mapErr(err)
	}
	if parentDepth+1 > req.MaxDepth {
		return ProjectEnvironmentBranch{}, ErrProjectEnvironmentBranchDepth
	}
	if req.MaxBranches > 0 {
		// Concurrent creates in other projects of the account count too.
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('project_environment_branches:' || $1::text))`, req.AccountID); err != nil {
			return ProjectEnvironmentBranch{}, mapErr(err)
		}
		var count int
		if err := tx.QueryRow(ctx, `select count(*) from project_environment_branches where account_id = $1`, req.AccountID).Scan(&count); err != nil {
			return ProjectEnvironmentBranch{}, mapErr(err)
		}
		if count >= req.MaxBranches {
			return ProjectEnvironmentBranch{}, ErrProjectEnvironmentBranchLimit
		}
	}
	var point any
	if !req.BranchPoint.IsZero() {
		point = req.BranchPoint.UTC()
	}
	var expired bool
	if err := tx.QueryRow(ctx, `select $1::timestamptz <= now()`, req.ExpiresAt.UTC()).Scan(&expired); err != nil {
		return ProjectEnvironmentBranch{}, mapErr(err)
	}
	if expired {
		return ProjectEnvironmentBranch{}, ErrInvalidArgument
	}
	branch := ProjectEnvironmentBranch{EnvironmentID: created.ID, AccountID: req.AccountID, ProjectID: req.ProjectID, Slug: created.Slug,
		ParentEnvironmentID: parentID, ParentSlug: req.ParentSlug, EgressMode: req.EgressMode, Depth: parentDepth + 1}
	err = tx.QueryRow(ctx, `
		insert into project_environment_branches
		    (environment_id, account_id, project_id, parent_environment_id, branch_point, expires_at, egress_mode, depth)
		values ($1, $2, $3, $4, coalesce($5::timestamptz, now()), $6, $7, $8)
		returning branch_point, expires_at, created_at
	`, created.ID, req.AccountID, req.ProjectID, parentID, point, req.ExpiresAt.UTC(), req.EgressMode, branch.Depth).Scan(
		&branch.BranchPoint, &branch.ExpiresAt, &branch.CreatedAt)
	if err != nil {
		return ProjectEnvironmentBranch{}, mapErr(err)
	}
	branch.BranchPoint, branch.ExpiresAt, branch.CreatedAt = branch.BranchPoint.UTC(), branch.ExpiresAt.UTC(), branch.CreatedAt.UTC()
	return branch, nil
}

func (s *PgStore) ProjectEnvironmentBranchBySlug(ctx context.Context, accountID, projectID, slug string) (ProjectEnvironmentBranch, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return ProjectEnvironmentBranch{}, ErrNotFound
	}
	if _, err := uuid.Parse(projectID); err != nil {
		return ProjectEnvironmentBranch{}, ErrNotFound
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `select exists (select 1 from project_environments where account_id = $1 and project_id = $2 and slug = $3)`,
		accountID, projectID, slug).Scan(&exists); err != nil {
		return ProjectEnvironmentBranch{}, mapErr(err)
	}
	if !exists {
		return ProjectEnvironmentBranch{}, ErrNotFound
	}
	branch, err := scanProjectEnvironmentBranch(s.pool.QueryRow(ctx, projectEnvironmentBranchSelect+`
		 where b.account_id = $1 and b.project_id = $2 and e.slug = $3`, accountID, projectID, slug))
	if errors.Is(err, ErrNotFound) {
		return ProjectEnvironmentBranch{}, ErrProjectEnvironmentNotBranch
	}
	return branch, err
}

func (s *PgStore) ListProjectEnvironmentBranches(ctx context.Context, accountID, projectID string) ([]ProjectEnvironmentBranch, error) {
	return s.listProjectEnvironmentBranches(ctx, projectEnvironmentBranchSelect+`
		 where b.account_id = $1 and b.project_id = $2
		 order by b.created_at, b.environment_id`, accountID, projectID)
}

func (s *PgStore) ExpiredProjectEnvironmentBranches(ctx context.Context, now time.Time, limit int) ([]ProjectEnvironmentBranch, error) {
	if limit <= 0 {
		return nil, ErrInvalidArgument
	}
	return s.listProjectEnvironmentBranches(ctx, projectEnvironmentBranchSelect+`
		 where b.expires_at <= $1
		   and not exists (select 1 from project_environment_branches c where c.parent_environment_id = b.environment_id)
		 order by b.depth desc, b.expires_at, b.environment_id
		 limit $2`, now.UTC(), limit)
}

func (s *PgStore) listProjectEnvironmentBranches(ctx context.Context, query string, args ...any) ([]ProjectEnvironmentBranch, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []ProjectEnvironmentBranch
	for rows.Next() {
		b, err := scanProjectEnvironmentBranch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, mapErr(rows.Err())
}

func (s *PgStore) ProjectEnvironmentBranchWorkloads(ctx context.Context, accountID, projectID, slug string) ([]ProjectEnvironmentBranchWorkload, error) {
	branch, err := s.ProjectEnvironmentBranchBySlug(ctx, accountID, projectID, slug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		select id::text, slug from apps
		 where account_id = $1 and project_id = $2 and status <> 'deleted' and preview_of_slug is null
		 order by slug`, accountID, projectID)
	if err != nil {
		return nil, mapErr(err)
	}
	slugs := map[string]string{}
	var ids []string
	for rows.Next() {
		var id, workloadSlug string
		if err := rows.Scan(&id, &workloadSlug); err != nil {
			rows.Close()
			return nil, mapErr(err)
		}
		slugs[id] = workloadSlug
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	var out []ProjectEnvironmentBranchWorkload
	for _, id := range ids {
		artifact, err := branchSourceArtifactDB(ctx, s.pool, accountID, projectID, branch.ParentSlug, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, ProjectEnvironmentBranchWorkload{AppID: id, WorkloadSlug: slugs[id], SourceDeploymentID: artifact.ID})
	}
	return out, nil
}

// branchSourceArtifactDB reads the parent's selected live artifact for one
// app, the same selection a full clone captures.
func branchSourceArtifactDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, parentSlug, appID string) (projectCloneArtifact, error) {
	q := new(sqlc.Queries)
	release, err := q.ReadProjectEnvironmentCloneSourceRelease(ctx, db, sqlc.ReadProjectEnvironmentCloneSourceReleaseParams{
		ProjectID: mustPgUUID(projectID), Environment: parentSlug})
	if err != nil {
		return projectCloneArtifact{}, mapErr(err)
	}
	scopesJSON, err := projectCloneValueScopesForAppsDB(ctx, db, ProjectEnvironmentClone{AccountID: accountID, ProjectID: projectID, SourceSlug: parentSlug}, []string{appID})
	if err != nil {
		return projectCloneArtifact{}, err
	}
	var scopes map[string]string
	if err := json.Unmarshal(scopesJSON, &scopes); err != nil {
		return projectCloneArtifact{}, err
	}
	raw, err := q.ReadProjectEnvironmentCloneSelectedArtifact(ctx, db, sqlc.ReadProjectEnvironmentCloneSelectedArtifactParams{
		AppID: mustPgUUID(appID), SourceScope: scopes[appID], ReleaseID: release})
	if errors.Is(err, pgx.ErrNoRows) {
		return projectCloneArtifact{}, ErrNotFound
	}
	if err != nil {
		return projectCloneArtifact{}, mapErr(err)
	}
	var artifact projectCloneArtifact
	if err := json.Unmarshal(raw, &artifact); err != nil || artifact.AppID != appID || artifact.ID == "" {
		return projectCloneArtifact{}, ErrConflict
	}
	if artifact.RootfsKey == "" || artifact.RootfsBytes <= 0 {
		return projectCloneArtifact{}, ErrNotFound
	}
	return normalizeProjectCloneArtifact(artifact), nil
}

func (s *PgStore) CreateDeploymentForEnvironmentBranch(ctx context.Context, accountID, projectID, slug, appID string) (Deployment, error) {
	if _, err := uuid.Parse(appID); err != nil {
		return Deployment{}, ErrNotFound
	}
	input := &projectEnvironmentBranchDeploymentInput{AccountID: accountID, ProjectID: projectID, Slug: slug}
	created, _, err := s.createDeployment(ctx, Deployment{AppID: appID, Scope: slug, branchInput: input}, nil, nil)
	return created, err
}

// prepareBranchDeploymentTx runs inside createDeployment under the app lock.
// It returns the deployment to insert, or the one this branch already
// created for the app.
func prepareBranchDeploymentTx(ctx context.Context, tx pgx.Tx, d Deployment) (Deployment, *Deployment, string, error) {
	input := d.branchInput
	var environmentID, parentSlug string
	err := tx.QueryRow(ctx, `
		select b.environment_id::text, p.slug
		  from project_environment_branches b
		  join project_environments e on e.id = b.environment_id
		  join project_environments p on p.id = b.parent_environment_id
		 where e.account_id = $1 and e.project_id = $2 and e.slug = $3
		 for share of b
	`, input.AccountID, input.ProjectID, input.Slug).Scan(&environmentID, &parentSlug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, nil, "", ErrProjectEnvironmentNotBranch
	}
	if err != nil {
		return Deployment{}, nil, "", mapErr(err)
	}
	if _, err := new(sqlc.Queries).ReadProjectEnvironmentCloneOwnedApp(ctx, tx, sqlc.ReadProjectEnvironmentCloneOwnedAppParams{
		AppID: mustPgUUID(d.AppID), AccountID: mustPgUUID(input.AccountID), ProjectID: mustPgUUID(input.ProjectID),
	}); err != nil {
		return Deployment{}, nil, "", mapErr(err)
	}
	var existingID string
	err = tx.QueryRow(ctx, `select deployment_id::text from project_environment_branch_deployments where environment_id = $1 and app_id = $2`,
		environmentID, d.AppID).Scan(&existingID)
	if err == nil {
		existing, err := deploymentByIDDB(ctx, tx, existingID)
		if err != nil {
			return Deployment{}, nil, "", err
		}
		return Deployment{}, &existing, "", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, nil, "", mapErr(err)
	}
	artifact, err := branchSourceArtifactDB(ctx, tx, input.AccountID, input.ProjectID, parentSlug, d.AppID)
	if err != nil {
		return Deployment{}, nil, "", err
	}
	// Serialize with layer GC: either the layers are still retained and GC
	// will see this deployment's reference, or the branch refuses the app.
	if err := requireDeploymentLayerArtifactsTx(ctx, tx, artifact.ID); err != nil {
		return Deployment{}, nil, "", err
	}
	return branchDeploymentFromArtifact(artifact, input.Slug, input), nil, artifact.ID, nil
}

// attachBranchDeploymentTx stamps the copied artifact, sidecar layers and
// lineage on the inserted row and moves it to snapshotting, where schedd's
// prime activates it without a boot.
func attachBranchDeploymentTx(ctx context.Context, tx pgx.Tx, input projectEnvironmentBranchDeploymentInput, sourceID string, d Deployment, created *Deployment) error {
	q := new(sqlc.Queries)
	if err := q.SetProjectEnvironmentCloneDeploymentArtifact(ctx, tx, sqlc.SetProjectEnvironmentCloneDeploymentArtifactParams{
		DeploymentID: mustPgUUID(created.ID), RootfsPath: d.RootfsPath, RootfsKey: d.RootfsKey, RootfsBytes: d.RootfsBytes,
		SignalKnown: d.SecretReloadSignalKnown, Signal: d.SecretReloadSignal, RuntimeBase: d.RuntimeBase,
	}); err != nil {
		return mapErr(err)
	}
	layers, err := q.ReadProjectEnvironmentCloneSidecarLayers(ctx, tx, mustPgUUID(sourceID))
	if err != nil {
		return mapErr(err)
	}
	for _, raw := range layers {
		var layer DeploymentSidecarLayer
		if err := json.Unmarshal(raw, &layer); err != nil {
			return ErrConflict
		}
		if err := q.InsertProjectEnvironmentCloneSidecarLayer(ctx, tx, sqlc.InsertProjectEnvironmentCloneSidecarLayerParams{
			DeploymentID: mustPgUUID(created.ID), SidecarName: layer.SidecarName, StorageKey: layer.StorageKey, Bytes: layer.Bytes, ContentDigest: layer.ContentDigest,
		}); err != nil {
			return mapErr(err)
		}
	}
	signals, err := q.ReadProjectEnvironmentCloneSidecarSignals(ctx, tx, mustPgUUID(sourceID))
	if err != nil {
		return mapErr(err)
	}
	for _, signal := range signals {
		if err := q.InsertProjectEnvironmentCloneSidecarSignal(ctx, tx, sqlc.InsertProjectEnvironmentCloneSidecarSignalParams{
			DeploymentID: mustPgUUID(created.ID), SidecarName: signal.SidecarName, Signal: signal.Signal,
		}); err != nil {
			return mapErr(err)
		}
	}
	tag, err := tx.Exec(ctx, `
		insert into project_environment_branch_deployments (deployment_id, environment_id, app_id, source_deployment_id)
		select $1, b.environment_id, $2, $3
		  from project_environment_branches b
		  join project_environments e on e.id = b.environment_id
		 where e.account_id = $4 and e.project_id = $5 and e.slug = $6
	`, created.ID, created.AppID, sourceID, input.AccountID, input.ProjectID, input.Slug)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	activated, err := scanDeploymentWithRootfs(tx.QueryRow(ctx, `
		update deployments
		   set status = 'snapshotting',
		       stage_state = jsonb_build_object(
		           'current', 'snapshot_prepare',
		           'current_started_at', to_jsonb(clock_timestamp()),
		           'history', coalesce(stage_state->'history', '[]'::jsonb)
		       )
		 where id = $1 and status = 'pending'
		 returning `+deploymentSelectColumnsWithRootfs, created.ID))
	if err != nil {
		return fmt.Errorf("state: stage environment branch deployment: %w", mapErr(err))
	}
	*created = activated
	return nil
}

func (s *PgStore) EnvironmentBranchDeployment(ctx context.Context, deploymentID string) (bool, error) {
	if _, err := uuid.Parse(deploymentID); err != nil {
		return false, nil
	}
	var exists bool
	err := s.pool.QueryRow(ctx, `select exists (select 1 from project_environment_branch_deployments where deployment_id = $1)`, deploymentID).Scan(&exists)
	return exists, mapErr(err)
}

func (s *PgStore) DeleteProjectEnvironmentBranch(ctx context.Context, accountID, projectID, slug string) error {
	_, err := retryTrafficSerialization(ctx, func() (ProjectEnvironmentCleanupJob, error) {
		return s.deleteProjectEnvironmentOnce(ctx, accountID, projectID, slug, ProjectEnvironmentCleanupResources{}, "", 0, true)
	})
	return err
}

// retireProjectEnvironmentBranchDeploymentsTx ends every deployment in the
// branch's scope so the ordinary environment delete can proceed: in-flight
// rows are cancelled and live rows superseded.
func retireProjectEnvironmentBranchDeploymentsTx(ctx context.Context, tx pgx.Tx, accountID, projectID, slug string) error {
	var environmentID string
	err := tx.QueryRow(ctx, `
		select b.environment_id::text
		  from project_environment_branches b
		  join project_environments e on e.id = b.environment_id
		 where e.account_id = $1 and e.project_id = $2 and e.slug = $3
		 for update of b
	`, accountID, projectID, slug).Scan(&environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrProjectEnvironmentNotBranch
	}
	if err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		update deployments d
		   set status = 'cancelled', cancelled_at = clock_timestamp(),
		       cancelled_by_principal = 'system:environment-branch', cancel_reason = 'system'
		  from apps a
		 where d.app_id = a.id and a.account_id = $1 and a.project_id = $2 and d.scope = $3
		   and d.status in ('pending', 'building', 'imaging', 'snapshotting')
	`, accountID, projectID, slug); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		update deployments d
		   set status = 'superseded', traffic_percent = 0
		  from apps a
		 where d.app_id = a.id and a.account_id = $1 and a.project_id = $2 and d.scope = $3
		   and d.status = 'live'
	`, accountID, projectID, slug); err != nil {
		return mapErr(err)
	}
	return nil
}
