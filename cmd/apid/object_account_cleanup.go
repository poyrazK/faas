package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Grace expiry is irreversible: RestoreAccount rejects it in both stores.
// Keep ownership metadata until every native bucket is confirmed deleted.
func (s *server) cleanupExpiredAccountObjectBuckets(ctx context.Context, account state.Account) error {
	current, err := s.store.AccountByID(ctx, account.ID)
	if err != nil {
		return err
	}
	if current.Status != state.AccountDeletedPending || current.DeletionRequestedAt == nil ||
		current.DeletionRequestedAt.Add(state.DeletionGraceDuration()).After(time.Now()) {
		return state.ErrConflict
	}
	store, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		return nil
	}
	cleanup, ok := s.store.(state.ObjectAccountBucketCleanupStore)
	if !ok {
		return state.ErrConflict
	}
	buckets, err := cleanup.ListAccountObjectBuckets(ctx, account.ID, api.ObjectOwnedCleanupBucketBatch)
	if err != nil {
		return err
	}
	var deferred error
	for _, bucket := range buckets {
		if s.objectStorage == nil {
			return objectstorage.ErrConfiguration
		}
		if bucket.State == "provisioning" {
			deferred = state.ErrConflict
			continue
		}
		if err := s.cleanupDevSessionBucket(ctx, store, bucket); err != nil {
			deferred = errors.Join(deferred, err)
		}
	}
	if len(buckets) == api.ObjectOwnedCleanupBucketBatch {
		deferred = errors.Join(deferred, objectstorage.ErrCleanupPending)
	}
	return deferred
}
