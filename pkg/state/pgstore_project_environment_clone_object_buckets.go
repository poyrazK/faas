package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ReserveProjectEnvironmentCloneObjectBucket(ctx context.Context, lease ProjectEnvironmentCloneLease, appID, sourceID string, limit int) (ObjectBucket, bool, error) {
	if !validCloneLeaseIdentity(lease) || appID == "" || sourceID == "" || limit < 1 {
		return ObjectBucket{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectBucket{}, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	identity := lease.Operation
	op, err := lockCloneWorkloadOperationTx(ctx, tx, identity.AccountID, identity.ProjectID, identity.ID)
	if err != nil {
		return ObjectBucket{}, false, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return ObjectBucket{}, false, err
	}
	if op.Status != CloneOperationCapturing {
		return ObjectBucket{}, false, ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return ObjectBucket{}, false, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return ObjectBucket{}, false, err
	}
	want, source, err := capturedCloneBucketReservation(op, views, appID, sourceID)
	if err != nil {
		return ObjectBucket{}, false, err
	}
	reserved, created, err := reserveObjectBucketTx(ctx, tx, want, limit)
	if err != nil {
		return ObjectBucket{}, false, err
	}
	if err := validateCloneBucketReservation(want, reserved, source); err != nil {
		return ObjectBucket{}, false, err
	}
	return reserved, created, tx.Commit(ctx)
}

func (s *PgStore) ProjectEnvironmentCloneObjectBucketForLease(ctx context.Context, lease ProjectEnvironmentCloneLease, appID, sourceID, targetID string) (ObjectBucket, error) {
	if !validCloneLeaseIdentity(lease) || appID == "" || sourceID == "" || targetID == "" {
		return ObjectBucket{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectBucket{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	identity := lease.Operation
	op, err := lockCloneWorkloadOperationTx(ctx, tx, identity.AccountID, identity.ProjectID, identity.ID)
	if err != nil {
		return ObjectBucket{}, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return ObjectBucket{}, err
	}
	if op.Status != CloneOperationCapturing && op.Status != CloneOperationCopying && op.Status != CloneOperationPublishing {
		return ObjectBucket{}, ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return ObjectBucket{}, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return ObjectBucket{}, err
	}
	want, source, err := capturedCloneBucketReservation(op, views, appID, sourceID)
	if err != nil {
		return ObjectBucket{}, err
	}
	row, err := new(sqlc.Queries).ReadProjectEnvironmentCloneObjectBucket(ctx, tx, sqlc.ReadProjectEnvironmentCloneObjectBucketParams{
		AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(appID), ID: mustPgUUID(targetID), EnvironmentCloneOperationID: mustPgUUID(op.ID),
	})
	if err != nil {
		return ObjectBucket{}, mapErr(err)
	}
	actual := objectBucketFromSQL(row)
	if err := validateCloneBucketReservation(want, actual, source); err != nil {
		return ObjectBucket{}, err
	}
	return actual, tx.Commit(ctx)
}
