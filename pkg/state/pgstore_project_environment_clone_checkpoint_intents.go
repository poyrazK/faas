package state

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) AdvanceProjectEnvironmentCloneCheckpointIntent(ctx context.Context, lease ProjectEnvironmentCloneLease, resources []ProjectEnvironmentCloneResource) (ProjectEnvironmentCloneOperation, error) {
	var zero ProjectEnvironmentCloneOperation
	if !validCloneLeaseIdentity(lease) {
		return zero, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	requested := lease.Operation
	op, err := lockCloneWorkloadOperationTx(ctx, tx, requested.AccountID, requested.ProjectID, requested.ID)
	if err != nil {
		return zero, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return zero, err
	}
	if err := validateCloneCheckpointIntentTransition(op, resources); err != nil {
		return zero, err
	}
	raw, err := json.Marshal(resources)
	if err != nil {
		return zero, ErrInvalidArgument
	}
	count, err := sqlc.New().AdvanceProjectEnvironmentCloneCheckpointIntent(ctx, tx, sqlc.AdvanceProjectEnvironmentCloneCheckpointIntentParams{
		OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID),
		ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(lease.Token), Resources: raw,
	})
	if err != nil {
		return zero, mapErr(err)
	}
	if count != 1 {
		return zero, ErrConflict
	}
	updated, err := cloneWorkerOperationDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, mapErr(err)
	}
	return updated, nil
}
