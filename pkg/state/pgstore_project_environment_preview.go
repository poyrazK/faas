package state

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var _ ProjectEnvironmentPreviewLifecycleStore = (*PgStore)(nil)

func (s *PgStore) UpdateProjectEnvironmentPreviewHead(
	ctx context.Context, accountID, projectID string, prNumber int, headSHA string, expiresAt time.Time,
) (ProjectEnvironment, error) {
	if !validProjectEnvironmentPreviewIdentity(prNumber, headSHA, false) || !expiresAt.After(time.Now()) {
		return ProjectEnvironment{}, ErrInvalidArgument
	}
	row := s.pool.QueryRow(ctx, `
		update project_environments e
		   set preview_head_sha = $4, preview_state = 'open', preview_expires_at = $5, updated_at = now()
		  from projects p
		 where p.id = e.project_id and p.account_id = $1
		   and e.project_id = $2 and e.preview_pr_number = $3
	   and e.preview_state <> 'tearing_down'
		returning e.id, e.account_id, e.project_id, e.slug, e.protected,
		          coalesce(e.preview_pr_number, 0), coalesce(e.preview_head_sha, ''),
		          coalesce(e.preview_state, ''), e.preview_expires_at, e.created_at, e.updated_at
	`, accountID, projectID, prNumber, strings.ToLower(headSHA), expiresAt.UTC())
	environment, err := scanProjectEnvironment(row)
	if !errors.Is(err, ErrNotFound) {
		return environment, err
	}
	existing, lookupErr := s.ProjectEnvironmentByPreviewPR(ctx, accountID, projectID, prNumber)
	if lookupErr != nil {
		return ProjectEnvironment{}, lookupErr
	}
	if existing.PreviewState == ProjectEnvironmentPreviewTearingDown {
		return ProjectEnvironment{}, ErrConflict
	}
	return ProjectEnvironment{}, ErrNotFound
}

func (s *PgStore) CloseProjectEnvironmentPreview(
	ctx context.Context, accountID, projectID string, prNumber int, closedUntil time.Time,
) (ProjectEnvironment, error) {
	if prNumber <= 0 || !closedUntil.After(time.Now()) {
		return ProjectEnvironment{}, ErrInvalidArgument
	}
	row := s.pool.QueryRow(ctx, `
		update project_environments e
		   set preview_state = 'closed',
		       preview_expires_at = case when e.preview_state = 'open' then $4 else e.preview_expires_at end,
		       updated_at = now()
		  from projects p
		 where p.id = e.project_id and p.account_id = $1
		   and e.project_id = $2 and e.preview_pr_number = $3
	   and e.preview_state in ('open', 'closed')
		returning e.id, e.account_id, e.project_id, e.slug, e.protected,
		          coalesce(e.preview_pr_number, 0), coalesce(e.preview_head_sha, ''),
		          coalesce(e.preview_state, ''), e.preview_expires_at, e.created_at, e.updated_at
	`, accountID, projectID, prNumber, closedUntil.UTC())
	environment, err := scanProjectEnvironment(row)
	if !errors.Is(err, ErrNotFound) {
		return environment, err
	}
	existing, lookupErr := s.ProjectEnvironmentByPreviewPR(ctx, accountID, projectID, prNumber)
	if lookupErr != nil {
		return ProjectEnvironment{}, lookupErr
	}
	if existing.PreviewState == ProjectEnvironmentPreviewTearingDown {
		return ProjectEnvironment{}, ErrConflict
	}
	return ProjectEnvironment{}, ErrNotFound
}

func (s *PgStore) ListProjectEnvironmentPreviewsForTeardown(
	ctx context.Context, now time.Time, limit int,
) ([]ProjectEnvironment, error) {
	if now.IsZero() || limit <= 0 {
		return nil, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx, `
		select id, account_id, project_id, slug, protected,
		       coalesce(preview_pr_number, 0), coalesce(preview_head_sha, ''),
		       coalesce(preview_state, ''), preview_expires_at, created_at, updated_at
		  from project_environments
		 where preview_pr_number is not null and preview_expires_at <= $1
		 order by preview_expires_at asc, id asc
		 limit $2
	`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProjectEnvironments(rows)
}

func (s *PgStore) BeginProjectEnvironmentPreviewTeardown(
	ctx context.Context, accountID, projectID, slug string, now, drainUntil time.Time,
) (ProjectEnvironment, []ProjectEnvironmentPreviewDeployment, error) {
	if slug == "" || now.IsZero() || !drainUntil.After(now) {
		return ProjectEnvironment{}, nil, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProjectEnvironment{}, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var environment ProjectEnvironment
	var previewExpiresAt sql.NullTime
	err = tx.QueryRow(ctx, `
		select e.id, e.account_id, e.project_id, e.slug, e.protected,
		       coalesce(e.preview_pr_number, 0), coalesce(e.preview_head_sha, ''),
		       coalesce(e.preview_state, ''), e.preview_expires_at, e.created_at, e.updated_at
		  from project_environments e
		  join projects p on p.id = e.project_id
		 where p.account_id = $1 and e.project_id = $2 and e.slug = $3
		 for update of e
	`, accountID, projectID, slug).Scan(
		&environment.ID, &environment.AccountID, &environment.ProjectID, &environment.Slug, &environment.Protected,
		&environment.PreviewPRNumber, &environment.PreviewHeadSHA, &environment.PreviewState,
		&previewExpiresAt, &environment.CreatedAt, &environment.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectEnvironment{}, nil, ErrNotFound
		}
		return ProjectEnvironment{}, nil, mapErr(err)
	}
	if previewExpiresAt.Valid {
		environment.PreviewExpiresAt = &previewExpiresAt.Time
	}
	if environment.PreviewPRNumber <= 0 || environment.PreviewExpiresAt == nil {
		return ProjectEnvironment{}, nil, ErrConflict
	}
	alreadyTearingDown := environment.PreviewState == ProjectEnvironmentPreviewTearingDown
	if environment.PreviewExpiresAt.After(now) {
		return ProjectEnvironment{}, nil, ErrConflict
	}
	if !alreadyTearingDown {
		row := tx.QueryRow(ctx, `
			update project_environments
			   set preview_state = 'tearing_down', preview_expires_at = $2, updated_at = now()
			 where id = $1
			returning id, account_id, project_id, slug, protected,
			          coalesce(preview_pr_number, 0), coalesce(preview_head_sha, ''),
			          coalesce(preview_state, ''), preview_expires_at, created_at, updated_at
		`, environment.ID, drainUntil.UTC())
		environment, err = scanProjectEnvironment(row)
		if err != nil {
			return ProjectEnvironment{}, nil, err
		}
	}
	rows, err := tx.Query(ctx, `
		with live as (
			select d.id, d.app_id
			  from deployments d
			  join apps a on a.id = d.app_id
			 where a.account_id = $1 and a.project_id = $2
			   and d.scope = $3 and d.status = 'live'
			 for update of d
		), superseded as (
			update deployments d
			   set status = 'superseded', traffic_percent = 0
			  from live
			 where d.id = live.id
			returning d.id, d.app_id
		)
		select id, app_id from superseded order by id
	`, accountID, projectID, slug)
	if err != nil {
		return ProjectEnvironment{}, nil, mapErr(err)
	}
	deployments := make([]ProjectEnvironmentPreviewDeployment, 0)
	for rows.Next() {
		var deployment ProjectEnvironmentPreviewDeployment
		if err := rows.Scan(&deployment.DeploymentID, &deployment.AppID); err != nil {
			rows.Close()
			return ProjectEnvironment{}, nil, mapErr(err)
		}
		deployments = append(deployments, deployment)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ProjectEnvironment{}, nil, mapErr(err)
	}
	rows.Close()
	if alreadyTearingDown && len(deployments) > 0 {
		row := tx.QueryRow(ctx, `
			update project_environments
			   set preview_expires_at = $2, updated_at = now()
			 where id = $1
			returning id, account_id, project_id, slug, protected,
			          coalesce(preview_pr_number, 0), coalesce(preview_head_sha, ''),
			          coalesce(preview_state, ''), preview_expires_at, created_at, updated_at
		`, environment.ID, drainUntil.UTC())
		environment, err = scanProjectEnvironment(row)
		if err != nil {
			return ProjectEnvironment{}, nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironment{}, nil, err
	}
	return environment, deployments, nil
}
