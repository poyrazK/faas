package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) RollbackProjectEnvironmentClone(ctx context.Context, accountID, projectID, slug string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("state: begin project environment clone rollback: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var projectSlug string
	err = tx.QueryRow(ctx, `
		select p.slug
		  from project_environments e
		  join projects p on p.id = e.project_id
		 where p.account_id = $1 and e.project_id = $2 and e.slug = $3
		 for update
	`, accountID, projectID, slug).Scan(&projectSlug)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return mapErr(err)
	}
	if slug == "production" {
		return ErrConflict
	}
	var hasDeployments, hasManagedSecrets bool
	if err := tx.QueryRow(ctx, `
		select exists (
			select 1 from apps a join deployments d on d.app_id = a.id
			 where a.account_id = $1 and a.project_id = $2 and d.scope = $3
		), exists (
			select 1 from apps a join app_secrets s on s.app_id = a.id
			 where a.account_id = $1 and a.project_id = $2 and s.scope = $3
			   and (s.managed_postgres_binding_id is not null
			        or s.managed_object_storage_credential_id is not null)
		)
	`, accountID, projectID, slug).Scan(&hasDeployments, &hasManagedSecrets); err != nil {
		return mapErr(err)
	}
	if hasDeployments || hasManagedSecrets {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		delete from app_envs e using apps a
		 where e.app_id = a.id and a.account_id = $1 and a.project_id = $2 and e.scope = $3
	`, accountID, projectID, slug); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		delete from app_secrets s using apps a
		 where s.app_id = a.id and a.account_id = $1 and a.project_id = $2 and s.scope = $3
	`, accountID, projectID, slug); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		delete from project_environment_config_versions
		 where account_id = $1 and project_id = $2 and environment_slug = $3
	`, accountID, projectID, slug); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		delete from project_environment_approvals
		 where account_id = $1 and project_slug = $2 and environment_slug = $3
	`, accountID, projectSlug, slug); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `delete from project_environments where project_id = $1 and slug = $2`, projectID, slug); err != nil {
		return mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: commit project environment clone rollback: %w", err)
	}
	return nil
}
