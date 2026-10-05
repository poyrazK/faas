package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) CloneProjectEnvironment(ctx context.Context, clone ProjectEnvironmentClone, limits api.Limits) (ProjectEnvironment, ProjectEnvironmentCloneResult, error) {
	return s.cloneProjectEnvironment(ctx, clone, limits, nil)
}

func (s *PgStore) cloneProjectEnvironment(ctx context.Context, clone ProjectEnvironmentClone, limits api.Limits, lease *ProjectEnvironmentCloneLease) (ProjectEnvironment, ProjectEnvironmentCloneResult, error) {
	for {
		created, result, err := s.cloneProjectEnvironmentOnce(ctx, clone, limits, lease)
		if err == nil {
			return created, result, nil
		}
		if ctx.Err() != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ctx.Err()
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.SerializationFailure {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
		// A source/runtime writer can commit after the policy snapshot starts.
		// The failed attempt has rolled back and released its session and pool
		// connection. Retry the complete clone with a fresh guarded snapshot.
		timer := time.NewTimer(api.TrafficPolicyMutationLockRetry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *PgStore) cloneProjectEnvironmentOnce(ctx context.Context, clone ProjectEnvironmentClone, limits api.Limits, lease *ProjectEnvironmentCloneLease) (ProjectEnvironment, ProjectEnvironmentCloneResult, error) {
	tx, err := s.beginTrafficPolicyMutation(ctx, uuidToPgtype(clone.AccountID))
	if err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, fmt.Errorf("state: begin project environment clone: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := lockProjectEnvironmentCloneReservationTx(ctx, tx, clone); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, mapProjectCloneSnapshotErr(err)
	}
	if lease != nil {
		op, err := lockCloneWorkloadOperationTx(ctx, tx, clone.AccountID, clone.ProjectID, clone.CloneOperationID)
		if err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
		if err := authorizeCloneObjectMutationTx(ctx, tx, op, lease); err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
		if err := requireCloneMaterializationCaptureTx(ctx, tx, op); err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
		created, err := replayCloneMaterializationTx(ctx, tx, op)
		if err == nil {
			if err := authorizeCloneObjectMutationTx(ctx, tx, op, lease); err != nil {
				return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
			}
			return created, ProjectEnvironmentCloneResult{}, tx.Commit(ctx)
		}
		if !errors.Is(err, ErrNotFound) {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
	}
	if err := lockProjectEnvironmentCloneSource(ctx, tx, clone); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, mapProjectCloneSnapshotErr(err)
	}
	var flagSnapshot projectCloneFeatureFlags
	if clone.CloneOperationID != "" {
		records, err := cloneWorkloadRecordsDB(ctx, tx, clone.AccountID, clone.ProjectID, clone.CloneOperationID)
		if err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
		config, err := capturedCloneProjectConfig(records)
		if err != nil || config.FeatureFlags == nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
		}
		flagSnapshot = *config.FeatureFlags
		// Authenticate the original environment identity without rereading flags.
		id, err := sqlc.New().ReadProjectEnvironmentCloneFlagScope(ctx, tx, sqlc.ReadProjectEnvironmentCloneFlagScopeParams{
			AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID), Environment: clone.SourceSlug,
		})
		if err != nil || uuidString(id) != flagSnapshot.SourceEnvironmentID {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
		}
		clone.capturedValueScopes = map[string]string{}
		for _, record := range records {
			clone.capturedValueScopes[record.AppID] = record.SourceScope
		}
		clone.capturedValues, err = capturedCloneValues(records)
		if err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
		clone.capturedPolicies, err = capturedCloneScopedPolicies(records)
		if err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
	} else {
		source, err := readCloneFeatureFlagsTx(ctx, tx, clone.AccountID, clone.ProjectID, clone.SourceSlug)
		if err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
		flagSnapshot, err = captureCloneFeatureFlags(source)
		if err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
	}
	clone.sourceValueScopesJSON, err = projectCloneValueScopesTx(ctx, tx, clone)
	if err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, mapProjectCloneSnapshotErr(err)
	}
	if clone.ExpectedSourceValuesHash != "" {
		var hash string
		if clone.capturedValues != nil {
			hash, err = clone.capturedValuesHash(clone.capturedValueScopes)
		} else {
			hash, err = projectCloneValuesHashTx(ctx, tx, clone)
		}
		if err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
		if hash != clone.ExpectedSourceValuesHash {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, ErrConflict
		}
	}
	if clone.ManagedBindingsPrepared {
		if err := checkPreparedProjectEnvironmentBindings(ctx, tx, clone); err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, mapProjectCloneSnapshotErr(err)
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
	if err := copyCloneFeatureFlagsTx(ctx, tx, created, flagSnapshot); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	if err := copyProjectEnvironmentWorkloadSpecs(ctx, tx, clone, created); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	result, err := copyProjectEnvironmentRows(ctx, tx, clone)
	if err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	if err := validateClonedEnvironmentPolicyProjections(ctx, tx, clone); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
	}
	if lease != nil {
		if err := saveCloneMaterializationTx(ctx, tx, lease.Operation, created); err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
		// Waiting on intent/resource locks cannot extend an expired worker.
		if err := authorizeCloneObjectMutationTx(ctx, tx, lease.Operation, lease); err != nil {
			return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironment{}, ProjectEnvironmentCloneResult{}, fmt.Errorf("state: commit project environment clone: %w", mapProjectCloneSnapshotErr(err))
	}
	return created, result, nil
}

func validateClonedEnvironmentPolicyProjections(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) error {
	oversized, err := sqlc.New().FindOversizedClonedEnvironmentPolicy(ctx, tx, sqlc.FindOversizedClonedEnvironmentPolicyParams{
		AccountID: uuidToPgtype(clone.AccountID), ProjectID: uuidToPgtype(clone.ProjectID),
		EnvironmentSlug: clone.TargetSlug, MaxBytes: api.TrafficPolicyMaxContractBytes})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("state: validate cloned traffic policies: %w", mapErr(err))
	}
	return checkTrafficProjectionSize(oversized.Scope, oversized.Observed)
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
	if clone.capturedValues != nil {
		bindings := map[string]bool{}
		for _, values := range clone.capturedValues {
			for _, secret := range values.Secrets {
				if id := cloneSecretManagedID(secret); id != "" {
					bindings[id] = true
				}
			}
		}
		bindingCount = len(bindings)
	} else if err := tx.QueryRow(ctx, `
		select count(distinct coalesce(s.managed_postgres_binding_id, s.managed_object_storage_credential_id))
		  from apps a join app_secrets s on s.app_id = a.id
		 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and a.preview_of_slug is null
		   and s.scope = ($3::jsonb ->> a.id::text)
		   and (s.managed_postgres_binding_id is not null or s.managed_object_storage_credential_id is not null)
	`, clone.AccountID, clone.ProjectID, clone.sourceValueScopesJSON).Scan(&bindingCount); err != nil {
		return mapErr(err)
	}
	if bindingCount != len(unique) {
		return ErrConflict
	}
	if clone.CloneOperationID != "" {
		if err := checkCapturedClonePostgresPreparationsTx(ctx, tx, clone); err != nil {
			return err
		}
		return checkCapturedCloneObjectPreparationsTx(ctx, tx, clone)
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
				 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and a.preview_of_slug is null and e.scope = $3
			) or exists (
				select 1 from apps a join app_secrets s on s.app_id = a.id
				 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and a.preview_of_slug is null and s.scope = $3
				   and ((s.managed_postgres_binding_id is null and s.managed_object_storage_credential_id is null)
				        or (s.managed_postgres_binding_id is not null and s.managed_object_storage_credential_id is not null)
				        or coalesce(s.managed_postgres_binding_id, s.managed_object_storage_credential_id)::text <> all($4::text[]))
			), (
				select count(*) from apps a join app_secrets s on s.app_id = a.id
				 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and a.preview_of_slug is null and s.scope = $3
				   and coalesce(s.managed_postgres_binding_id, s.managed_object_storage_credential_id)::text = any($4::text[])
			), (
				select count(distinct coalesce(s.managed_postgres_binding_id, s.managed_object_storage_credential_id))
				  from apps a join app_secrets s on s.app_id = a.id
				 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and a.preview_of_slug is null and s.scope = $3
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
			 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and a.preview_of_slug is null and e.scope = $3
		) or exists (
			select 1 from apps a join app_secrets s on s.app_id = a.id
			 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and a.preview_of_slug is null and s.scope = $3
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
	q := sqlc.New()
	// Reference mutations and the executor take the source before application
	// and catalog locks. Retain that order while reading the clone snapshot.
	if _, err := q.LockProjectEnvironmentCloneGitSources(ctx, tx, sqlc.LockProjectEnvironmentCloneGitSourcesParams{
		AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID), SourceSlug: clone.SourceSlug, TargetSlug: clone.TargetSlug,
	}); err != nil {
		return mapErr(err)
	}
	if _, err := q.LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{
		AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID),
	}); err != nil {
		return mapErr(err)
	}
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
	if clone.capturedValues != nil {
		count := 0
		for _, values := range clone.capturedValues {
			for _, secret := range values.Secrets {
				if cloneSecretManagedID(secret) != "" {
					count++
				}
			}
		}
		if count > 0 {
			return &ProjectEnvironmentCloneManagedBindingsError{ManagedSecretCount: count}
		}
		return nil
	}
	var count int
	err := tx.QueryRow(ctx, `
		select count(*)
		  from app_secrets s
		  join apps a on a.id = s.app_id
		 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and a.preview_of_slug is null
		   and s.scope = ($3::jsonb ->> a.id::text)
		   and (s.managed_postgres_binding_id is not null
		        or s.managed_object_storage_credential_id is not null)
	`, clone.AccountID, clone.ProjectID, clone.sourceValueScopesJSON).Scan(&count)
	if err != nil {
		return mapErr(err)
	}
	if count > 0 {
		return &ProjectEnvironmentCloneManagedBindingsError{ManagedSecretCount: count}
	}
	return nil
}

func checkProjectEnvironmentCloneQuota(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone, limits api.Limits) error {
	if clone.capturedValues != nil {
		return checkCapturedProjectEnvironmentCloneQuota(ctx, tx, clone, limits)
	}
	rows, err := sqlc.New().ProjectEnvironmentCloneQuota(ctx, tx, sqlc.ProjectEnvironmentCloneQuotaParams{
		AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID), SourceSlug: clone.SourceSlug,
		SourceValueScopes: []byte(clone.sourceValueScopesJSON),
	})
	if err != nil {
		return mapErr(err)
	}
	for _, row := range rows {
		observedSecrets := int(row.SecretCount + row.SourceSecrets)
		if clone.ManagedBindingsPrepared {
			observedSecrets -= int(row.SourceManaged)
		}
		if limits.SecretCountMax > 0 && observedSecrets > limits.SecretCountMax {
			return &ProjectEnvironmentCloneQuotaError{WorkloadSlug: row.Slug, Resource: "secrets", Limit: limits.SecretCountMax, Observed: observedSecrets}
		}
		envCount, sourceEnv := int(row.EnvCount), int(row.SourceEnv)
		if limits.EnvVarsMax > 0 && envCount+sourceEnv > limits.EnvVarsMax {
			return &ProjectEnvironmentCloneQuotaError{WorkloadSlug: row.Slug, Resource: "variables", Limit: limits.EnvVarsMax, Observed: envCount + sourceEnv}
		}
		observedSuppressions := int(row.SuppressionCount + row.SourceSuppressions)
		if observedSuppressions > api.EnvironmentSecretReferenceSuppressionsMaxPerApp {
			return &ProjectEnvironmentCloneQuotaError{WorkloadSlug: row.Slug, Resource: "secret_reference_suppressions", Limit: api.EnvironmentSecretReferenceSuppressionsMaxPerApp, Observed: observedSuppressions}
		}
	}
	return nil
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
	if err := tx.QueryRow(ctx, `select count(*) from apps where account_id = $1 and project_id = $2 and status <> 'deleted' and preview_of_slug is null`, clone.AccountID, clone.ProjectID).Scan(&result.WorkloadsCopied); err != nil {
		return result, mapErr(err)
	}
	configurationCopied, err := copyProjectEnvironmentConfig(ctx, tx, clone)
	if err != nil {
		return result, err
	}
	result.ConfigurationCopied = configurationCopied
	result.VariablesCopied, result.SecretsCopied, err = copyProjectEnvironmentScopedValues(ctx, tx, clone)
	if err != nil {
		return result, err
	}
	if clone.capturedPolicies != nil {
		if err := copyCapturedProjectEnvironmentScopedPolicies(ctx, tx, clone, &result); err != nil {
			return result, err
		}
		if result.RoutesCopied < result.WorkloadsCopied {
			result.SharedResources = append(result.SharedResources, "routes")
		}
		result.SharedResources = append(result.SharedResources, "policies")
		return result, nil
	}
	if clone.capturedValues == nil {
		refsCopied, err := sqlc.New().CopyProjectEnvironmentSecretReferences(ctx, tx, sqlc.CopyProjectEnvironmentSecretReferencesParams{
			AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID), SourceSlug: clone.SourceSlug, TargetSlug: clone.TargetSlug,
		})
		if err != nil {
			return result, mapErr(err)
		}
		result.SecretReferencesCopied = int(refsCopied)
		if err := sqlc.New().CopyProjectEnvironmentSecretSuppressions(ctx, tx, sqlc.CopyProjectEnvironmentSecretSuppressionsParams{AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID), SourceSlug: clone.SourceSlug, TargetSlug: clone.TargetSlug}); err != nil {
			return result, mapErr(err)
		}
	}
	if err := tx.QueryRow(ctx, `
		with copied as (
			insert into project_environment_route_policies
			    (account_id, project_id, app_id, environment_slug, only_allow_declared_routes, declared_routes)
			select a.account_id, a.project_id, a.id, $4,
			       coalesce(p.only_allow_declared_routes, a.only_declared_routes),
			       coalesce(p.declared_routes, a.declared_routes, '[]'::jsonb)
			  from apps a
			  left join project_environment_route_policies p
			    on p.app_id = a.id and p.environment_slug = $3
		 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and a.preview_of_slug is null
		   and not (coalesce(p.only_allow_declared_routes, a.only_declared_routes)
		            and jsonb_array_length(coalesce(p.declared_routes, a.declared_routes, '[]'::jsonb)) = 0)
			returning 1
		)
		select count(*) from copied
	`, clone.AccountID, clone.ProjectID, clone.SourceSlug, clone.TargetSlug).Scan(&result.RoutesCopied); err != nil {
		return result, mapErr(err)
	}
	if result.RoutesCopied < result.WorkloadsCopied {
		result.SharedResources = append(result.SharedResources, "routes")
	}
	if err := tx.QueryRow(ctx, `
		with copied as (
			insert into project_environment_edge_policies
			    (account_id, project_id, app_id, environment_slug, rules)
			select p.account_id, p.project_id, p.app_id, $4, p.rules
			  from project_environment_edge_policies p
			  join apps a on a.id = p.app_id
			 where p.account_id = $1 and p.project_id = $2 and p.environment_slug = $3
			   and a.status <> 'deleted' and a.preview_of_slug is null
			returning 1
		)
		select count(*) from copied
	`, clone.AccountID, clone.ProjectID, clone.SourceSlug, clone.TargetSlug).Scan(&result.PoliciesCopied); err != nil {
		return result, mapErr(err)
	}
	result.SharedResources = append(result.SharedResources, "policies")
	return result, nil
}

func copyProjectEnvironmentConfig(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) (bool, error) {
	if clone.CloneOperationID != "" {
		records, err := cloneWorkloadRecordsDB(ctx, tx, clone.AccountID, clone.ProjectID, clone.CloneOperationID)
		if err != nil {
			return false, err
		}
		if len(records) > 0 {
			config, err := capturedCloneProjectConfig(records)
			if err != nil {
				return false, err
			}
			count, err := new(sqlc.Queries).InsertProjectEnvironmentCloneProjectConfiguration(ctx, tx, sqlc.InsertProjectEnvironmentCloneProjectConfigurationParams{
				AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID), Environment: clone.TargetSlug,
				ConfigHash: config.Hash, ConfigJson: config.Values,
			})
			return count == 1, mapErr(err)
		}
	}
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
	if clone.capturedValues != nil {
		return copyCapturedProjectEnvironmentCloneValues(ctx, tx, clone)
	}
	envTag, err := tx.Exec(ctx, `
		insert into app_envs (account_id, app_id, scope, key, value)
		select e.account_id, e.app_id, $4, e.key, e.value
		  from app_envs e join apps a on a.id = e.app_id
		 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and a.preview_of_slug is null
		   and e.scope = ($3::jsonb ->> a.id::text)
	`, clone.AccountID, clone.ProjectID, clone.sourceValueScopesJSON, clone.TargetSlug)
	if err != nil {
		return 0, 0, mapErr(err)
	}
	secretTag, err := tx.Exec(ctx, `
		insert into app_secrets (account_id, app_id, scope, key, ciphertext, kid, value_hash, secret_version, secret_class)
		select s.account_id, s.app_id, $4, s.key, s.ciphertext, s.kid, s.value_hash, s.secret_version, s.secret_class
		  from app_secrets s join apps a on a.id = s.app_id
		 where a.account_id = $1 and a.project_id = $2 and a.status <> 'deleted' and a.preview_of_slug is null
		   and s.scope = ($3::jsonb ->> a.id::text)
		   and s.managed_postgres_binding_id is null and s.managed_object_storage_credential_id is null
	`, clone.AccountID, clone.ProjectID, clone.sourceValueScopesJSON, clone.TargetSlug)
	if err != nil {
		return 0, 0, mapErr(err)
	}
	return int(envTag.RowsAffected()), int(secretTag.RowsAffected()), nil
}
