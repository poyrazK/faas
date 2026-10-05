package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectMultipartPartURLStore = (*PgStore)(nil)

func (s *PgStore) RecordObjectMultipartPartURL(ctx context.Context, expected ObjectMultipartUpload, expires time.Time) error {
	if _, err := s.GetObjectMultipartUpload(ctx, expected.AccountID, expected.AppID, expected.BucketID, expected.ID); err != nil {
		return err
	}
	n, err := sqlc.New().ObjectMultipartRecordPartURL(ctx, s.pool, sqlc.ObjectMultipartRecordPartURLParams{
		ID: mustPgUUID(expected.ID), AccountID: mustPgUUID(expected.AccountID), AppID: mustPgUUID(expected.AppID), BucketID: mustPgUUID(expected.BucketID),
		ObjectKey: expected.Key, ProviderUploadID: expected.ProviderUploadID, SignedExpiresAt: objectUsageTime(expires),
		DrainSeconds: int32((api.ObjectTransferTimeout + api.ObjectMultipartCleanupGrace) / time.Second), MaxTtlSeconds: api.ObjectMultipartPartURLMaxTTLSeconds,
	})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
