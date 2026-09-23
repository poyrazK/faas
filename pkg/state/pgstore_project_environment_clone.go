package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func (s *PgStore) CloneProjectEnvironment(ctx context.Context, clone ProjectEnvironmentClone, limits api.Limits) (ProjectEnvironment, ProjectEnvironmentCloneResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, fmt.Errorf("state: begin project environment clone: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := lockProjectEnvironmentCloneSource(ctx, tx, clone); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	if clone.ManagedBindingsPrepared {
		if err := checkPreparedProjectEnvironmentBindings(ctx, tx, clone); err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
	}
	if err := checkProjectEnvironmentCloneTargetScope(ctx, tx, clone); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	if !clone.ShareResources && !clone.ManagedBindingsPrepared {
		if err := checkProjectEnvironmentCloneManagedBindings(ctx, tx, clone); err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
	}
	if err := checkProjectEnvironmentCloneQuota(ctx, tx, clone, limits); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	created, err := insertClonedProjectEnvironment(ctx, tx, clone)
	if err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	result, err := copyProjectEnvironmentRows(ctx, tx, clone)
	if err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, fmt.Errorf("state: commit project environment clone: %w", err)
	}
	return created, result, nil
}

func checkPreparedProjectEnvironmentBindings(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) error {
	unique := make(map[string]struct{}, len(clone.PreparedManagedBindingIDs))
	for _, id := range clone.PreparedManagedBindingIDs {
		unique[id] = struct{}{}
	}
	if len(unique) == 0 || len(unique) != len(clone.PreparedManagedBindingIDs) || clone.PreparedManagedSecretCount < len(unique) {
		return ErrConflict
	}
	var bindingCount int
	if err := tx.QueryRow(ctx, `
		select count(distinct coalesce(s.managed_postgres_binding_id, s.managed_object_storage_credential_id))
		  from apps a join app_secrets s on s.app_id = a.id
		 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and s.scope = $3
		   and (s.managed_postgres_binding_id is not null or s.managed_object_storage_credential_id is not null)
	`, clone.AccountID, clone.ProjectID, clone.SourceSlug).Scan(&bindingCount); err != nil {
		return mapErr(err)
	}
	if bindingCount != len(unique) {
		return ErrConflict
	}
	return nil
}

func checkProjectEnvironmentCloneTargetScope(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) error {
	if clone.ManagedBindingsPrepared {
		unique := make(map[string]struct{}, len(clone.PreparedManagedBindingIDs))
		for _, id := range clone.PreparedManagedBindingIDs {
			unique[id] = struct{}{}
		}
		if len(unique) == 0 || len(unique) != len(clone.PreparedManagedBindingIDs) || clone.PreparedManagedSecretCount < len(unique) {
			return ErrConflict
		}
		var hasUnmanagedScopeState bool
		var preparedSecretCount, preparedBindingCount int
		if err := tx.QueryRow(ctx, `
			select exists (
				select 1 from apps a join app_envs e on e.app_id = a.id
				 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and e.scope = $3
			) or exists (
				select 1 from apps a join app_secrets s on s.app_id = a.id
				 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and s.scope = $3
				   and ((s.managed_postgres_binding_id is null and s.managed_object_storage_credential_id is null)
				        or (s.managed_postgres_binding_id is not null and s.managed_object_storage_credential_id is not null)
				        or coalesce(s.managed_postgres_binding_id, s.managed_object_storage_credential_id)::text <> all($4::text[]))
			), (
				select count(*) from apps a join app_secrets s on s.app_id = a.id
				 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and s.scope = $3
				   and coalesce(s.managed_postgres_binding_id, s.managed_object_storage_credential_id)::text = any($4::text[])
			), (
				select count(distinct coalesce(s.managed_postgres_binding_id, s.managed_object_storage_credential_id))
				  from apps a join app_secrets s on s.app_id = a.id
				 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and s.scope = $3
				   and coalesce(s.managed_postgres_binding_id, s.managed_object_storage_credential_id)::text = any($4::text[])
			)
		`, clone.AccountID, clone.ProjectID, clone.TargetSlug, clone.PreparedManagedBindingIDs).Scan(&hasUnmanagedScopeState, &preparedSecretCount, &preparedBindingCount); err != nil {
			return mapErr(err)
		}
		if hasUnmanagedScopeState || preparedSecretCount != clone.PreparedManagedSecretCount || preparedBindingCount != len(unique) {
			return ErrConflict
		}
		return nil
	}
	var hasScopedState bool
	if err := tx.QueryRow(ctx, `
		select exists (
			select 1 from apps a join app_envs e on e.app_id = a.id
			 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and e.scope = $3
		) or exists (
			select 1 from apps a join app_secrets s on s.app_id = a.id
			 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and s.scope = $3
		)
	`, clone.AccountID, clone.ProjectID, clone.TargetSlug).Scan(&hasScopedState); err != nil {
		return mapErr(err)
	}
	if hasScopedState {
		return ErrConflict
	}
	return nil
}

func lockProjectEnvironmentCloneSource(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) error {
	var id string
	err := tx.QueryRow(ctx, `
		select e.id::text
		  from project_environments e
		  join projects p on p.id = e.project_id
		 where p.account_id = $1 and p.id = $2 and e.slug = $3
		 for update
	`, clone.AccountID, clone.ProjectID, clone.SourceSlug).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return mapErr(err)
}

func checkProjectEnvironmentCloneManagedBindings(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) error {
	var count int
	err := tx.QueryRow(ctx, `
		select count(*)
		  from app_secrets s
		  join apps a on a.id = s.app_id
		 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted'
		   and s.scope = $3
		   and (s.managed_postgres_binding_id is not null
		        or s.managed_object_storage_credential_id is not null)
	`, clone.AccountID, clone.ProjectID, clone.SourceSlug).Scan(&count)
	if err != nil {
		return mapErr(err)
	}
	if count > 0 {
		return &ProjectEnvironmentCloneManagedBindingsError{ManagedSecretCount: count}
	}
	return nil
}

func checkProjectEnvironmentCloneQuota(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone, limits api.Limits) error {
	rows, err := tx.Query(ctx, `
		select a.slug,
		       (select count(*) from app_secrets s where s.app_id = a.id),
		       (select count(*) from app_secrets s where s.app_id = a.id and s.scope = $3),
		       (select count(*) from app_secrets s where s.app_id = a.id and s.scope = $3 and (s.managed_postgres_binding_id is not null or s.managed_object_storage_credential_id is not null)),
		       (select count(*) from app_envs e where e.app_id = a.id),
		       (select count(*) from app_envs e where e.app_id = a.id and e.scope = $3)
		  from apps a
		 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted'
		 order by a.slug
	`, clone.AccountID, clone.ProjectID, clone.SourceSlug)
	if err != nil {
		return mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		var secretCount, sourceSecrets, sourceManaged, envCount, sourceEnv int
		if err := rows.Scan(&slug, &secretCount, &sourceSecrets, &sourceManaged, &envCount, &sourceEnv); err != nil {
			return mapErr(err)
		}
		observedSecrets := secretCount + sourceSecrets
		if clone.ManagedBindingsPrepared {
			observedSecrets -= sourceManaged
		}
		if limits.SecretCountMax > 0 && observedSecrets > limits.SecretCountMax {
			return &ProjectEnvironmentCloneQuotaError{WorkloadSlug: slug, Resource: "secrets", Limit: limits.SecretCountMax, Observed: observedSecrets}
		}
		if limits.EnvVarsMax > 0 && envCount+sourceEnv > limits.EnvVarsMax {
			return &ProjectEnvironmentCloneQuotaError{WorkloadSlug: slug, Resource: "variables", Limit: limits.EnvVarsMax, Observed: envCount + sourceEnv}
		}
	}
	return mapErr(rows.Err())
}

func insertClonedProjectEnvironment(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) (ProjectEnvironment, error) {
	row := tx.QueryRow(ctx, `
		insert into project_environments (account_id, project_id, slug, protected)
		values ($1, $2, $3, $4)
		returning id, account_id, project_id, slug, protected, created_at, updated_at
	`, clone.AccountID, clone.ProjectID, clone.TargetSlug, clone.TargetProtected)
	created, err := scanProjectEnvironment(row)
	return created, mapErr(err)
}

func copyProjectEnvironmentRows(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) (ProjectEnvironmentCloneResult, error) {
	result := ProjectEnvironmentCloneResult{}
	if err := tx.QueryRow(ctx, `select count(*) from apps where account_id = $1 and project_id = $2 and status <> 'deleted'`, clone.AccountID, clone.ProjectID).Scan(&result.WorkloadsCopied); err != nil {
		return result, mapErr(err)
	}
	configurationCopied, err := copyProjectEnvironmentConfig(ctx, tx, clone)
	if err != nil {
		return result, err
	}
	result.ConfigurationCopied = configurationCopied
	result.VariablesCopied, result.SecretsCopied, err = copyProjectEnvironmentScopedValues(ctx, tx, clone)
	return result, err
}

func copyProjectEnvironmentConfig(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) (bool, error) {
	configTag, err := tx.Exec(ctx, `
		insert into project_environment_config_versions
			(account_id, project_id, environment_slug, version, config_hash, config_json)
		select $1, $2, $4, 1, c.config_hash, c.config_json
		  from project_environment_config_versions c
		 where c.account_id = $1 and c.project_id = $2 and c.environment_slug = $3
		 order by c.version desc limit 1
	`, clone.AccountID, clone.ProjectID, clone.SourceSlug, clone.TargetSlug)
	if err != nil {
		return false, mapErr(err)
	}
	return configTag.RowsAffected() == 1, nil
}

func copyProjectEnvironmentScopedValues(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) (int, int, error) {
	envTag, err := tx.Exec(ctx, `
		insert into app_envs (account_id, app_id, scope, key, value)
		select e.account_id, e.app_id, $4, e.key, e.value
		  from app_envs e join apps a on a.id = e.app_id
		 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and e.scope = $3
	`, clone.AccountID, clone.ProjectID, clone.SourceSlug, clone.TargetSlug)
	if err != nil {
		return 0, 0, mapErr(err)
	}
	secretTag, err := tx.Exec(ctx, `
		insert into app_secrets (account_id, app_id, scope, key, ciphertext, kid, value_hash)
		select s.account_id, s.app_id, $4, s.key, s.ciphertext, s.kid, s.value_hash
		  from app_secrets s join apps a on a.id = s.app_id
		 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and s.scope = $3
		   and s.managed_postgres_binding_id is null and s.managed_object_storage_credential_id is null
	`, clone.AccountID, clone.ProjectID, clone.SourceSlug, clone.TargetSlug)
	if err != nil {
		return 0, 0, mapErr(err)
	}
	return int(envTag.RowsAffected()), int(secretTag.RowsAffected()), nil
}
