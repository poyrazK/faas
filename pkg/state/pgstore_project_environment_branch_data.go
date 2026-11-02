package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ADR-20261031193635 slice 2: the branch data reconciler's PostgreSQL state.

// checkProjectEnvironmentBranchPrivateNetworks refuses a branch of a project
// with a workload attached to a private network. The clone transaction holds
// the project's app rows, which an attachment write locks too, so neither can
// slip in beside the other.
func checkProjectEnvironmentBranchPrivateNetworks(ctx context.Context, tx pgx.Tx, accountID, projectID string) error {
	var attached bool
	err := tx.QueryRow(ctx, `
		select exists (
			select 1 from app_private_network_attachments n
			  join apps a on a.id = n.app_id
			 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and a.preview_of_slug is null
		)`, accountID, projectID).Scan(&attached)
	if err != nil {
		return mapErr(err)
	}
	if attached {
		return ErrProjectEnvironmentBranchPrivateNetwork
	}
	return nil
}

// checkProjectEnvironmentBranchManagedBindings counts the source's managed
// PostgreSQL and object-storage secrets. The branch data reconciler copies
// the databases and gives each bucket an overlay (slice 3), so both leave the
// branch's data pending.
func checkProjectEnvironmentBranchManagedBindings(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) (int, error) {
	var postgres, objects int
	err := tx.QueryRow(ctx, `
		select count(*) filter (where s.managed_postgres_binding_id is not null)::int,
		       count(*) filter (where s.managed_object_storage_credential_id is not null)::int
		  from app_secrets s
		  join apps a on a.id = s.app_id
		 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and a.preview_of_slug is null
		   and s.scope = ($3::jsonb ->> a.id::text)
	`, clone.AccountID, clone.ProjectID, clone.sourceValueScopesJSON).Scan(&postgres, &objects)
	if err != nil {
		return 0, mapErr(err)
	}
	return postgres + objects, nil
}

func (s *PgStore) ClaimProjectEnvironmentBranchData(ctx context.Context, leaseToken string, now time.Time, lease time.Duration) (ProjectEnvironmentBranch, error) {
	if _, err := uuid.Parse(leaseToken); err != nil || lease <= 0 {
		return ProjectEnvironmentBranch{}, ErrInvalidArgument
	}
	var environmentID string
	err := s.pool.QueryRow(ctx, `
		with due as (
			select environment_id from project_environment_branches
			 where data_state = 'pending' and data_next_attempt_at <= $1
			   and (data_lease_until is null or data_lease_until <= $1)
			 order by data_next_attempt_at, environment_id
			 limit 1
			 for update skip locked
		)
		update project_environment_branches b
		   set data_lease_token = $2, data_lease_until = $3
		  from due where b.environment_id = due.environment_id
		returning b.environment_id::text
	`, now.UTC(), leaseToken, now.UTC().Add(lease)).Scan(&environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironmentBranch{}, ErrNotFound
	}
	if err != nil {
		return ProjectEnvironmentBranch{}, mapErr(err)
	}
	return scanProjectEnvironmentBranch(s.pool.QueryRow(ctx, projectEnvironmentBranchSelect+` where b.environment_id = $1`, environmentID))
}

func (s *PgStore) CompleteProjectEnvironmentBranchData(ctx context.Context, environmentID, leaseToken string) error {
	return s.releaseProjectEnvironmentBranchData(ctx, `
		update project_environment_branches
		   set data_state = 'ready', data_error = '', data_lease_token = null, data_lease_until = null
		 where environment_id = $1 and data_lease_token = $2 and data_state = 'pending'`, environmentID, leaseToken)
}

func (s *PgStore) RetryProjectEnvironmentBranchData(ctx context.Context, environmentID, leaseToken string, next time.Time, message string, failed bool) error {
	state := EnvironmentBranchDataPending
	if failed {
		state = EnvironmentBranchDataFailed
	}
	return s.releaseProjectEnvironmentBranchData(ctx, `
		update project_environment_branches
		   set data_state = $5, data_error = $4, data_attempts = data_attempts + 1, data_next_attempt_at = $3,
		       data_lease_token = null, data_lease_until = null
		 where environment_id = $1 and data_lease_token = $2 and data_state = 'pending'`,
		environmentID, leaseToken, next.UTC(), message, state)
}

func (s *PgStore) releaseProjectEnvironmentBranchData(ctx context.Context, query string, args ...any) error {
	if _, err := uuid.Parse(args[0].(string)); err != nil {
		return ErrNotFound
	}
	if _, err := uuid.Parse(args[1].(string)); err != nil {
		return ErrConflict
	}
	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() != 1 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `select exists (select 1 from project_environment_branches where environment_id = $1)`, args[0]).Scan(&exists); err != nil {
			return mapErr(err)
		}
		if !exists {
			return ErrNotFound
		}
		return ErrConflict
	}
	return nil
}

const projectEnvironmentBranchDatabaseColumns = `id::text, account_id::text, project_id::text, environment_id::text, environment_slug,
	source_database_id::text, copy_name, coalesce(copy_database_id::text, ''), point_in_time,
	coalesce(lease_token::text, ''), created_at`

func scanProjectEnvironmentBranchDatabase(row pgx.Row) (ProjectEnvironmentBranchDatabase, error) {
	var d ProjectEnvironmentBranchDatabase
	err := row.Scan(&d.ID, &d.AccountID, &d.ProjectID, &d.EnvironmentID, &d.EnvironmentSlug, &d.SourceDatabaseID,
		&d.CopyName, &d.CopyDatabaseID, &d.PointInTime, &d.LeaseToken, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironmentBranchDatabase{}, ErrNotFound
	}
	if err != nil {
		return ProjectEnvironmentBranchDatabase{}, mapErr(err)
	}
	d.PointInTime, d.CreatedAt = d.PointInTime.UTC(), d.CreatedAt.UTC()
	return d, nil
}

func (s *PgStore) RecordProjectEnvironmentBranchDatabase(ctx context.Context, copy ProjectEnvironmentBranchDatabase) (ProjectEnvironmentBranchDatabase, error) {
	for _, id := range []string{copy.AccountID, copy.ProjectID, copy.EnvironmentID, copy.SourceDatabaseID} {
		if _, err := uuid.Parse(id); err != nil {
			return ProjectEnvironmentBranchDatabase{}, ErrInvalidArgument
		}
	}
	if copy.CopyName == "" || copy.EnvironmentSlug == "" || copy.PointInTime.IsZero() {
		return ProjectEnvironmentBranchDatabase{}, ErrInvalidArgument
	}
	recorded, err := scanProjectEnvironmentBranchDatabase(s.pool.QueryRow(ctx, `
		insert into project_environment_branch_databases
		    (account_id, project_id, environment_id, environment_slug, source_database_id, copy_name, point_in_time)
		values ($1, $2, $3, $4, $5, $6, $7)
		on conflict (environment_id, source_database_id) do nothing
		returning `+projectEnvironmentBranchDatabaseColumns,
		copy.AccountID, copy.ProjectID, copy.EnvironmentID, copy.EnvironmentSlug, copy.SourceDatabaseID, copy.CopyName, copy.PointInTime.UTC()))
	if !errors.Is(err, ErrNotFound) {
		return recorded, err
	}
	return scanProjectEnvironmentBranchDatabase(s.pool.QueryRow(ctx, `select `+projectEnvironmentBranchDatabaseColumns+`
		from project_environment_branch_databases where environment_id = $1 and source_database_id = $2`,
		copy.EnvironmentID, copy.SourceDatabaseID))
}

func (s *PgStore) SetProjectEnvironmentBranchDatabaseCopy(ctx context.Context, id, copyDatabaseID string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	if _, err := uuid.Parse(copyDatabaseID); err != nil {
		return ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `
		update project_environment_branch_databases set copy_database_id = $2
		 where id = $1 and (copy_database_id is null or copy_database_id = $2)`, id, copyDatabaseID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ListProjectEnvironmentBranchDatabases(ctx context.Context, environmentID string) ([]ProjectEnvironmentBranchDatabase, error) {
	if _, err := uuid.Parse(environmentID); err != nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `select `+projectEnvironmentBranchDatabaseColumns+`
		from project_environment_branch_databases where environment_id = $1 order by source_database_id`, environmentID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []ProjectEnvironmentBranchDatabase
	for rows.Next() {
		d, err := scanProjectEnvironmentBranchDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, mapErr(rows.Err())
}

func (s *PgStore) ClaimOrphanedProjectEnvironmentBranchDatabase(ctx context.Context, leaseToken string, now time.Time, lease time.Duration) (ProjectEnvironmentBranchDatabase, error) {
	if _, err := uuid.Parse(leaseToken); err != nil || lease <= 0 {
		return ProjectEnvironmentBranchDatabase{}, ErrInvalidArgument
	}
	return scanProjectEnvironmentBranchDatabase(s.pool.QueryRow(ctx, `
		with orphan as (
			select d.id from project_environment_branch_databases d
			 where not exists (select 1 from project_environment_branches b where b.environment_id = d.environment_id)
			   and (d.lease_until is null or d.lease_until <= $1)
			 order by d.id
			 limit 1
			 for update skip locked
		)
		update project_environment_branch_databases d
		   set lease_token = $2, lease_until = $3
		  from orphan where d.id = orphan.id
		returning `+projectEnvironmentBranchDatabaseColumnsPrefixed,
		now.UTC(), leaseToken, now.UTC().Add(lease)))
}

const projectEnvironmentBranchDatabaseColumnsPrefixed = `d.id::text, d.account_id::text, d.project_id::text, d.environment_id::text, d.environment_slug,
	d.source_database_id::text, d.copy_name, coalesce(d.copy_database_id::text, ''), d.point_in_time,
	coalesce(d.lease_token::text, ''), d.created_at`

func (s *PgStore) RemoveProjectEnvironmentBranchDatabase(ctx context.Context, id, leaseToken string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	if _, err := uuid.Parse(leaseToken); err != nil {
		return ErrConflict
	}
	tag, err := s.pool.Exec(ctx, `delete from project_environment_branch_databases where id = $1 and lease_token = $2`, id, leaseToken)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ProjectEnvironmentBranchDeploymentsAwaitingActivation(ctx context.Context, environmentID string) ([]Deployment, error) {
	if _, err := uuid.Parse(environmentID); err != nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		select l.deployment_id::text from project_environment_branch_deployments l
		  join deployments d on d.id = l.deployment_id
		 where l.environment_id = $1 and d.status = 'snapshotting'
		 order by l.deployment_id`, environmentID)
	if err != nil {
		return nil, mapErr(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, mapErr(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	out := make([]Deployment, 0, len(ids))
	for _, id := range ids {
		d, err := deploymentByIDDB(ctx, s.pool, id)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *PgStore) EnvironmentBranchDataReady(ctx context.Context, deploymentID string) (bool, error) {
	if _, err := uuid.Parse(deploymentID); err != nil {
		return true, nil
	}
	var state string
	err := s.pool.QueryRow(ctx, `
		select coalesce(b.data_state, 'missing') from project_environment_branch_deployments l
		  left join project_environment_branches b on b.environment_id = l.environment_id
		 where l.deployment_id = $1`, deploymentID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, mapErr(err)
	}
	return state == EnvironmentBranchDataReady, nil
}
