package main

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

const objectRecoveryInterval = 15 * time.Second

// Retry state is persisted, so requests, restarts and additional replicas
// cannot reset the cooldown. Configuration failures get a slow probe cadence.
func objectRetryPolicy(err error, attempt int32) (string, time.Duration) {
	if errors.Is(err, objectstorage.ErrConfiguration) {
		return "configuration", time.Hour
	}
	if errors.Is(err, objectstorage.ErrInvalid) {
		return "invalid", time.Hour
	}
	code := "temporary"
	if errors.Is(err, objectstorage.ErrConflict) {
		code = "conflict"
	}
	delay := 30 * time.Second
	for i := int32(1); i < attempt && delay < 15*time.Minute; i++ {
		delay *= 2
	}
	return code, min(delay, 15*time.Minute)
}

// executeBucketOperation accepts only a claimed row. It never discovers or
// deletes unidentified provider buckets. Lease-token fencing protects commits.
func (s *server) executeBucketOperation(ctx context.Context, st state.ObjectBucketStore, b state.ObjectBucket) error {
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		err = objectstorage.ErrConfiguration
	} else if b.State == "provisioning" {
		if !s.objectStorageEnabled() {
			err = objectstorage.ErrUnavailable
		} else {
			err = backend.Provider.CreateBucket(callCtx, b.PhysicalName)
		}
	} else {
		err = backend.Provider.DeleteBucket(callCtx, b.PhysicalName)
	}
	notEmpty := b.State == "deleting" && errors.Is(err, objectstorage.ErrNotEmpty)
	if err != nil && !notEmpty {
		return s.retryBucketOperation(ctx, st, b, err)
	}
	next, event := "ready", "object_bucket.created"
	if b.State == "deleting" {
		next, event = "deleted", "object_bucket.deleted"
	}
	if notEmpty {
		next = "ready"
	}
	if finishErr := finishBucket(ctx, st, b.ID, b.LeaseToken, next); finishErr != nil {
		return finishErr
	}
	if err == nil {
		s.audit.Emit(ctx, event, &b.AccountID, map[string]any{"app_id": b.AppID, "bucket_id": b.ID})
	}
	return err
}

func (s *server) retryBucketOperation(ctx context.Context, st state.ObjectBucketStore, b state.ObjectBucket, cause error) error {
	code, delay := objectRetryPolicy(cause, b.AttemptCount)
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := st.RetryObjectBucket(finishCtx, b.ID, b.LeaseToken, code, delay); err != nil {
		return err
	}
	// Only bounded codes, never upstream messages, keys or signed URLs.
	s.log.Warn("object storage operation deferred", "bucket_id", b.ID, "backend_id", b.BackendID, "operation", b.State, "error_code", code, "attempt", b.AttemptCount, "retry_in", delay, "needs_attention", b.AttemptCount >= 5 || code == "configuration" || code == "invalid")
	return cause
}

func (s *server) reconcileObjectBuckets(ctx context.Context, observe func(string, string)) error {
	st, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		return nil
	}
	rows, err := st.DueObjectBuckets(ctx, s.objectStorageEnabled(), 20)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if row.State == "provisioning" && !s.objectStorageEnabled() {
			continue
		}
		b, err := st.ClaimObjectBucketRecovery(ctx, row.AccountID, row.AppID, row.ID, uuid.NewString(), row.State)
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		err = s.executeBucketOperation(ctx, st, b)
		outcome := "success"
		if errors.Is(err, objectstorage.ErrNotEmpty) {
			outcome = "not_empty"
		} else if err != nil {
			outcome = "deferred"
		}
		if observe != nil {
			observe(b.State, outcome)
		}
	}
	return nil
}

func (s *server) reconcileObjectMultipartUploads(ctx context.Context, observe func(string, string)) error {
	store, ok := s.store.(state.ObjectMultipartUploadStore)
	if !ok || s.objectStorage == nil {
		return nil
	}
	rows, err := store.DueObjectMultipartUploads(ctx, 20)
	if err != nil {
		return err
	}
	buckets, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		return nil
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		// A recent initiating intent pauses with the hot flag. Once it expires,
		// ensure/recover its provider identity so the following sweep can abort
		// it; cleanup must remain possible while new signing is disabled.
		if row.State == state.ObjectMultipartInitiating && !s.objectStorageEnabled() && row.ExpiresAt.After(time.Now()) {
			continue
		}
		operation := row.State
		if row.State == state.ObjectMultipartActive {
			operation = state.ObjectMultipartAborting
		}
		claimed, claimErr := store.ClaimObjectMultipartUpload(ctx, row.AccountID, row.AppID, row.BucketID, row.ID, uuid.NewString(), operation, row.Parts, true)
		if errors.Is(claimErr, state.ErrConflict) || errors.Is(claimErr, state.ErrNotFound) {
			continue
		}
		if claimErr != nil {
			return claimErr
		}
		bucket, bucketErr := buckets.GetObjectBucket(ctx, row.AccountID, row.AppID, row.BucketID)
		if bucketErr != nil {
			return bucketErr
		}
		execErr := s.executeObjectMultipartOperation(ctx, store, bucket, claimed)
		outcome := "success"
		if execErr != nil {
			outcome = "deferred"
		}
		if observe != nil {
			observe("multipart_"+operation, outcome)
		}
	}
	return nil
}

func (s *server) runObjectStorageRecovery(ctx context.Context) {
	attempts := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "faas_object_storage_recovery_attempts_total", Help: "Claimed object-storage recovery attempts by operation and outcome."}, []string{"operation", "outcome"})
	if s.ops != nil {
		s.ops.Registry().MustRegister(attempts)
	}
	observe := func(operation, outcome string) { attempts.WithLabelValues(operation, outcome).Inc() }
	ticker := time.NewTicker(objectRecoveryInterval)
	defer ticker.Stop()
	for {
		if err := s.reconcileObjectBuckets(ctx, observe); err != nil && ctx.Err() == nil {
			s.log.Warn("object storage recovery sweep failed")
		}
		if err := s.reconcileObjectMultipartUploads(ctx, observe); err != nil && ctx.Err() == nil {
			s.log.Warn("object storage multipart recovery sweep failed")
		}
		ticker.Reset(objectRecoveryInterval)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
