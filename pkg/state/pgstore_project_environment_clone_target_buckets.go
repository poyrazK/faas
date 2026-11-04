package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectEnvironmentCloneTargetBucketStore reads a clone's own target bucket
// and its versioning job. Until the clone publishes, its target is hidden from
// the customer bucket reads (GetObjectBucket, GetObjectBucketVersioning), so
// the clone worker reads it here (ADR-20261103160000).
type ProjectEnvironmentCloneTargetBucketStore interface {
	// ProjectEnvironmentCloneTargetBucket returns the live bucket the
	// operation reserved, or ErrNotFound once it is deleted.
	ProjectEnvironmentCloneTargetBucket(ctx context.Context, accountID, appID, operationID, bucketID string) (ObjectBucket, error)
	// ProjectEnvironmentCloneTargetVersioning returns the bucket's
	// versioning job, or ErrNotFound when none was ever requested.
	ProjectEnvironmentCloneTargetVersioning(ctx context.Context, bucketID string) (ObjectBucketVersioning, error)
}

func (s *PgStore) ProjectEnvironmentCloneTargetBucket(ctx context.Context, accountID, appID, operationID, bucketID string) (ObjectBucket, error) {
	if !validCloneCredentialSourceID(accountID) || !validCloneCredentialSourceID(appID) || !validCloneCredentialSourceID(operationID) || !validCloneCredentialSourceID(bucketID) {
		return ObjectBucket{}, ErrNotFound
	}
	row, err := sqlc.New().ReadProjectEnvironmentCloneObjectBucket(ctx, s.pool, sqlc.ReadProjectEnvironmentCloneObjectBucketParams{AccountID: mustPgUUID(accountID),
		AppID: mustPgUUID(appID), ID: mustPgUUID(bucketID), EnvironmentCloneOperationID: mustPgUUID(operationID)})
	if err != nil {
		return ObjectBucket{}, mapErr(err)
	}
	return objectBucketFromSQL(row), nil
}

func (s *PgStore) ProjectEnvironmentCloneTargetVersioning(ctx context.Context, bucketID string) (ObjectBucketVersioning, error) {
	if !validCloneCredentialSourceID(bucketID) {
		return ObjectBucketVersioning{}, ErrNotFound
	}
	return readObjectVersioning(ctx, s.pool, bucketID)
}
