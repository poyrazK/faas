package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneObjectWriteFenceStore = (*PgStore)(nil)

func cloneWriteFenceLeaseTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneOperation, error) {
	op, err := lockCloneWorkloadOperationTx(ctx, tx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil {
		return op, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return op, err
	}
	if op.Status != CloneOperationCapturing && op.Status != CloneOperationCompensating {
		return op, ErrConflict
	}
	return op, nil
}

func (s *PgStore) AcquireProjectEnvironmentCloneObjectWriteFence(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ObjectBucketWriteFence, error) {
	if !validCloneObjectFenceLease(lease) || !validCloneCredentialSourceID(sourceID) {
		return ObjectBucketWriteFence{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectBucketWriteFence{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := cloneWriteFenceLeaseTx(ctx, tx, lease)
	if err != nil {
		return ObjectBucketWriteFence{}, err
	}
	if op.Status != CloneOperationCapturing {
		return ObjectBucketWriteFence{}, ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return ObjectBucketWriteFence{}, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return ObjectBucketWriteFence{}, err
	}
	b, err := capturedCloneWriteFenceBucket(op, views, sourceID)
	if err != nil {
		return ObjectBucketWriteFence{}, err
	}
	b, err = lockObjectMutationBucket(ctx, tx, b)
	if err != nil {
		return ObjectBucketWriteFence{}, err
	}
	_, err = sqlc.New().CloneObjectWriteFenceInsert(ctx, tx, sqlc.CloneObjectWriteFenceInsertParams{
		BucketID: mustPgUUID(b.ID), OperationID: mustPgUUID(op.ID), BackendID: b.BackendID,
		BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(lease.Token)})
	if err != nil {
		return ObjectBucketWriteFence{}, mapErr(err)
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return ObjectBucketWriteFence{}, err
	}
	fence, err := readOwnedObjectWriteFenceTx(ctx, tx, b, op.ID, op.ID)
	if err != nil {
		return ObjectBucketWriteFence{}, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return ObjectBucketWriteFence{}, err
	}
	return fence, mapErr(tx.Commit(ctx))
}

func cloneObjectWriteFencesTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) ([]ObjectBucketWriteFence, error) {
	rows, err := sqlc.New().CloneObjectWriteFenceBuckets(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ObjectBucketWriteFence, 0, len(rows))
	for _, row := range rows {
		b, err := lockObjectMutationBucket(ctx, tx, objectBucketFromSQL(row))
		if err != nil {
			return nil, err
		}
		fence, err := readOwnedObjectWriteFenceTx(ctx, tx, b, op.ID, op.ID)
		if err != nil || b.AccountID != op.AccountID {
			if err == nil {
				err = ErrConflict
			}
			return nil, err
		}
		out = append(out, fence)
	}
	return out, nil
}

func (s *PgStore) ProjectEnvironmentCloneObjectWriteFencesForLease(ctx context.Context, lease ProjectEnvironmentCloneLease) ([]ObjectBucketWriteFence, error) {
	if !validCloneObjectFenceLease(lease) {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := cloneWriteFenceLeaseTx(ctx, tx, lease)
	if err != nil {
		return nil, err
	}
	fences, err := cloneObjectWriteFencesTx(ctx, tx, op)
	if err != nil {
		return nil, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return nil, err
	}
	return fences, mapErr(tx.Commit(ctx))
}

func (s *PgStore) AbandonProjectEnvironmentCloneObjectWriteFences(ctx context.Context, lease ProjectEnvironmentCloneLease) error {
	if !validCloneObjectFenceLease(lease) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := cloneWriteFenceLeaseTx(ctx, tx, lease)
	if err != nil {
		return err
	}
	if op.Status != CloneOperationCompensating {
		return ErrConflict
	}
	fences, err := cloneObjectWriteFencesTx(ctx, tx, op)
	if err != nil {
		return err
	}
	for _, fence := range fences {
		b := fence.Bucket
		n, err := sqlc.New().CloneObjectWriteFenceDelete(ctx, tx, sqlc.CloneObjectWriteFenceDeleteParams{
			BucketID: mustPgUUID(b.ID), OperationID: mustPgUUID(op.ID), BackendID: b.BackendID,
			BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(lease.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}
