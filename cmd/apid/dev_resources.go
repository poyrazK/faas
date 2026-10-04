package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Developer sessions are leased previews. Reclaiming their buckets before
// tombstoning the app keeps a vanished CLI from leaving a physical bucket and
// an app row that cannot pass the permanent-purge bucket guard.
func (s *server) cleanupDevSessionResources(ctx context.Context, app state.App) error {
	if err := s.cleanupDevSessionBuckets(ctx, app); err != nil {
		return err
	}
	return s.cleanupDevPostgres(ctx, app)
}

func (s *server) cleanupDevSessionBuckets(ctx context.Context, app state.App) error {
	if app.PreviewOfSlug == "" || app.PreviewPrNumber != 0 {
		return nil
	}
	store, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		return nil
	}
	buckets, err := store.ListObjectBuckets(ctx, app.AccountID, app.ID)
	if err != nil {
		return fmt.Errorf("list developer session buckets: %w", err)
	}
	if len(buckets) == 0 {
		return nil
	}
	if s.objectStorage == nil {
		return objectstorage.ErrConfiguration
	}
	for _, bucket := range buckets {
		if bucket.State == "provisioning" {
			// The normal recovery worker must settle an in-progress create
			// before a provider bucket can be safely removed.
			return state.ErrConflict
		}
		if err := s.cleanupDevSessionBucket(ctx, store, bucket); err != nil {
			return fmt.Errorf("delete developer session bucket %s: %w", bucket.Name, err)
		}
	}
	return nil
}

func (s *server) cleanupDevSessionBucket(ctx context.Context, store state.ObjectBucketStore, bucket state.ObjectBucket) error {
	if bindings, ok := s.store.(state.ObjectS3CredentialBindingStore); ok {
		credentials, err := bindings.ListObjectS3Credentials(ctx, bucket.AccountID, bucket.ID)
		if err != nil {
			return err
		}
		for _, credential := range credentials {
			if credential.ManagedAppID == bucket.AppID && credential.RotationParentID == "" {
				if _, err := bindings.RevokeObjectS3ComputeBinding(ctx, bucket.AccountID, bucket.ID, credential.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
					return err
				}
			}
		}
	}
	token := uuid.NewString()
	var claimed state.ObjectBucket
	var err error
	if bucket.State == "deleting" {
		claimed, err = store.ClaimObjectBucketRecovery(ctx, bucket.AccountID, bucket.AppID, bucket.ID, token, "deleting")
	} else {
		claimed, err = store.ClaimObjectBucket(ctx, bucket.AccountID, bucket.AppID, bucket.ID, token, "deleting")
	}
	if err != nil {
		return err
	}
	return s.executeBucketOperation(ctx, store, claimed)
}
