package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func cloneWorkerOperationDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, operationID string) (ProjectEnvironmentCloneOperation, error) {
	r, err := new(sqlc.Queries).ReadProjectEnvironmentCloneWorkerOperation(ctx, db, sqlc.ReadProjectEnvironmentCloneWorkerOperationParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), OperationID: mustPgUUID(operationID),
	})
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, mapErr(err)
	}
	op := ProjectEnvironmentCloneOperation{ID: r.ID, AccountID: r.AccountID, ProjectID: r.ProjectID,
		SourceEnvironment: r.SourceEnvironment, TargetEnvironment: r.TargetEnvironment, IdempotencyKey: r.IdempotencyKey,
		SourceRevisionHash: r.SourceRevisionHash, SourceReleaseSetID: r.SourceReleaseSetID, TargetReleaseSetID: r.TargetReleaseSetID,
		Status: r.Status, Revision: r.Revision, ErrorCode: r.ErrorCode, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
	if err := json.Unmarshal(r.Resources, &op.Resources); err != nil {
		return ProjectEnvironmentCloneOperation{}, ErrConflict
	}
	return op, nil
}

func (s *PgStore) ClaimNextProjectEnvironmentClone(ctx context.Context, token string, ttl time.Duration) (ProjectEnvironmentCloneLease, error) {
	if !validCloneLeaseToken(token) || !validCloneLeaseDuration(ttl) {
		return ProjectEnvironmentCloneLease{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentCloneLease{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := new(sqlc.Queries)
	project, err := q.LockNextProjectEnvironmentCloneWorkerProject(ctx, tx)
	if err != nil {
		return ProjectEnvironmentCloneLease{}, mapErr(err)
	}
	r, err := q.ClaimProjectEnvironmentCloneInProject(ctx, tx, sqlc.ClaimProjectEnvironmentCloneInProjectParams{
		AccountID: mustPgUUID(project.AccountID), ProjectID: mustPgUUID(project.ProjectID), LeaseToken: mustPgUUID(token), LeaseMicroseconds: ttl.Microseconds(),
	})
	if err != nil {
		return ProjectEnvironmentCloneLease{}, mapErr(err)
	}
	op, err := cloneWorkerOperationDB(ctx, tx, project.AccountID, project.ProjectID, r.OperationID)
	if err != nil {
		return ProjectEnvironmentCloneLease{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentCloneLease{}, mapErr(err)
	}
	return ProjectEnvironmentCloneLease{Operation: op, Token: token, ExpiresAt: r.LeaseUntil.Time, AttemptCount: r.AttemptCount}, nil
}

func (s *PgStore) RenewProjectEnvironmentCloneLease(ctx context.Context, lease ProjectEnvironmentCloneLease, ttl time.Duration) (ProjectEnvironmentCloneLease, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneLeaseDuration(ttl) {
		return ProjectEnvironmentCloneLease{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentCloneLease{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op := lease.Operation
	if _, err := lockCloneWorkloadOperationTx(ctx, tx, op.AccountID, op.ProjectID, op.ID); err != nil {
		return ProjectEnvironmentCloneLease{}, err
	}
	r, err := new(sqlc.Queries).RenewProjectEnvironmentCloneWorkerLease(ctx, tx, sqlc.RenewProjectEnvironmentCloneWorkerLeaseParams{
		AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), OperationID: mustPgUUID(op.ID),
		LeaseToken: mustPgUUID(lease.Token), Revision: op.Revision, Status: op.Status, LeaseMicroseconds: ttl.Microseconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironmentCloneLease{}, ErrConflict
	}
	if err != nil {
		return ProjectEnvironmentCloneLease{}, mapErr(err)
	}
	op, err = cloneWorkerOperationDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return ProjectEnvironmentCloneLease{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentCloneLease{}, mapErr(err)
	}
	return ProjectEnvironmentCloneLease{Operation: op, Token: lease.Token, ExpiresAt: r.LeaseUntil.Time, AttemptCount: r.AttemptCount}, nil
}

func (s *PgStore) ReleaseProjectEnvironmentCloneLease(ctx context.Context, lease ProjectEnvironmentCloneLease, retryAfter time.Duration) error {
	if !validCloneLeaseIdentity(lease) || retryAfter < 0 || retryAfter%time.Microsecond != 0 {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op := lease.Operation
	if _, err := lockCloneWorkloadOperationTx(ctx, tx, op.AccountID, op.ProjectID, op.ID); err != nil {
		return err
	}
	count, err := new(sqlc.Queries).ReleaseProjectEnvironmentCloneWorkerLease(ctx, tx, sqlc.ReleaseProjectEnvironmentCloneWorkerLeaseParams{
		AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), OperationID: mustPgUUID(op.ID),
		LeaseToken: mustPgUUID(lease.Token), Revision: op.Revision, Status: op.Status, RetryMicroseconds: retryAfter.Microseconds(),
	})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}
