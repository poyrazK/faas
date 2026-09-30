package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const projectEnvironmentCloneOperationColumns = `id, account_id, project_id, source_environment, target_environment,
	idempotency_key, source_revision_hash, coalesce(source_release_set_id::text, ''),
	status, revision, resources, error_code, created_at, updated_at, coalesce(target_release_set_id::text, '')`

func scanProjectEnvironmentCloneOperation(row pgx.Row) (ProjectEnvironmentCloneOperation, error) {
	var op ProjectEnvironmentCloneOperation
	var raw []byte
	if err := row.Scan(&op.ID, &op.AccountID, &op.ProjectID, &op.SourceEnvironment,
		&op.TargetEnvironment, &op.IdempotencyKey, &op.SourceRevisionHash,
		&op.SourceReleaseSetID, &op.Status, &op.Revision, &raw, &op.ErrorCode,
		&op.CreatedAt, &op.UpdatedAt, &op.TargetReleaseSetID); err != nil {
		return ProjectEnvironmentCloneOperation{}, mapErr(err)
	}
	if err := json.Unmarshal(raw, &op.Resources); err != nil {
		return ProjectEnvironmentCloneOperation{}, fmt.Errorf("state: decode project environment clone resources: %w", err)
	}
	return op, nil
}

func (s *PgStore) CreateProjectEnvironmentCloneOperation(ctx context.Context, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneOperation, error) {
	if err := validateProjectEnvironmentCloneOperation(op); err != nil || (op.Status != "" && op.Status != CloneOperationPending) {
		return ProjectEnvironmentCloneOperation{}, ErrInvalidProjectEnvironmentCloneOperation
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := new(sqlc.Queries)
	if _, err := q.LockProjectEnvironmentCloneProject(ctx, tx, sqlc.LockProjectEnvironmentCloneProjectParams{
		AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID),
	}); err != nil {
		return ProjectEnvironmentCloneOperation{}, mapErr(err)
	}
	existing, err := scanProjectEnvironmentCloneOperation(tx.QueryRow(ctx, `
		select `+projectEnvironmentCloneOperationColumns+` from project_environment_clone_operations
		where account_id = $1 and project_id = $2 and idempotency_key = $3`, op.AccountID, op.ProjectID, op.IdempotencyKey))
	if err == nil {
		if existing.SourceEnvironment == op.SourceEnvironment && existing.TargetEnvironment == op.TargetEnvironment &&
			existing.SourceRevisionHash == op.SourceRevisionHash && existing.SourceReleaseSetID == op.SourceReleaseSetID {
			return existing, nil
		}
		return ProjectEnvironmentCloneOperation{}, ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return ProjectEnvironmentCloneOperation{}, err
	}
	presence, err := q.ReadProjectEnvironmentCloneEnvironmentPresence(ctx, tx, sqlc.ReadProjectEnvironmentCloneEnvironmentPresenceParams{
		ProjectID: mustPgUUID(op.ProjectID), SourceEnvironment: op.SourceEnvironment, TargetEnvironment: op.TargetEnvironment,
	})
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, mapErr(err)
	}
	if !presence.SourceExists {
		return ProjectEnvironmentCloneOperation{}, ErrNotFound
	}
	if presence.TargetExists {
		return ProjectEnvironmentCloneOperation{}, ErrConflict
	}
	created, err := scanProjectEnvironmentCloneOperation(tx.QueryRow(ctx, `
		insert into project_environment_clone_operations
			(account_id, project_id, source_environment, target_environment,
			 idempotency_key, source_revision_hash, source_release_set_id)
		values ($1, $2, $3, $4, $5, $6, nullif($7, '')::uuid)
		returning `+projectEnvironmentCloneOperationColumns,
		op.AccountID, op.ProjectID, op.SourceEnvironment, op.TargetEnvironment,
		op.IdempotencyKey, op.SourceRevisionHash, op.SourceReleaseSetID))
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentCloneOperation{}, mapErr(err)
	}
	return created, nil
}

func (s *PgStore) ProjectEnvironmentCloneOperationByID(ctx context.Context, accountID, projectID, id string) (ProjectEnvironmentCloneOperation, error) {
	return scanProjectEnvironmentCloneOperation(s.pool.QueryRow(ctx, `
		select `+projectEnvironmentCloneOperationColumns+`
		  from project_environment_clone_operations
		 where id = $1 and account_id = $2 and project_id = $3
	`, id, accountID, projectID))
}

func (s *PgStore) ProjectEnvironmentCloneOperationByIdempotencyKey(ctx context.Context, accountID, projectID, key string) (ProjectEnvironmentCloneOperation, error) {
	return scanProjectEnvironmentCloneOperation(s.pool.QueryRow(ctx, `
		select `+projectEnvironmentCloneOperationColumns+`
		  from project_environment_clone_operations
		 where account_id = $1 and project_id = $2 and idempotency_key = $3
	`, accountID, projectID, key))
}

func (s *PgStore) AdvanceProjectEnvironmentCloneOperation(ctx context.Context, accountID, projectID, id, expectedStatus, nextStatus string, expectedRevision int64, resources []ProjectEnvironmentCloneResource, errorCode string) (ProjectEnvironmentCloneOperation, error) {
	if expectedRevision < 1 || !validCloneOperationTransition(expectedStatus, nextStatus) || !validCloneOperationErrorCode(errorCode) {
		return ProjectEnvironmentCloneOperation{}, ErrInvalidProjectEnvironmentCloneOperation
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	current, err := lockCloneWorkloadOperationTx(ctx, tx, accountID, projectID, id)
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, err
	}
	if current.Status != expectedStatus || current.Revision != expectedRevision {
		return ProjectEnvironmentCloneOperation{}, ErrConflict
	}
	if err := validateCloneResourceTransition(current, nextStatus, resources, errorCode); err != nil {
		return ProjectEnvironmentCloneOperation{}, err
	}
	if nextStatus == CloneOperationPublishing || nextStatus == CloneOperationReady {
		if err := verifyClonePublicationTx(ctx, tx, current, resources); err != nil {
			return ProjectEnvironmentCloneOperation{}, err
		}
		if nextStatus == CloneOperationReady {
			for _, resource := range resources {
				if resource.Kind == "workload" && current.TargetReleaseSetID == "" {
					return ProjectEnvironmentCloneOperation{}, ErrConflict
				}
			}
		}
	}
	if resources == nil {
		resources = []ProjectEnvironmentCloneResource{}
	}
	raw, err := json.Marshal(resources)
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, fmt.Errorf("state: encode project environment clone resources: %w", err)
	}
	updated, err := scanProjectEnvironmentCloneOperation(tx.QueryRow(ctx, `
		update project_environment_clone_operations
		   set status = $5, revision = revision + 1, resources = $7::jsonb, error_code = $8, updated_at = now()
		 where id = $1 and account_id = $2 and project_id = $3 and status = $4 and revision = $6
		returning `+projectEnvironmentCloneOperationColumns,
		id, accountID, projectID, expectedStatus, nextStatus, expectedRevision, raw, errorCode))
	if errors.Is(err, ErrNotFound) {
		if _, lookupErr := s.ProjectEnvironmentCloneOperationByID(ctx, accountID, projectID, id); lookupErr == nil {
			return ProjectEnvironmentCloneOperation{}, ErrConflict
		} else if !errors.Is(lookupErr, ErrNotFound) {
			return ProjectEnvironmentCloneOperation{}, lookupErr
		}
	}
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentCloneOperation{}, mapErr(err)
	}
	return updated, nil
}
