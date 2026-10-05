package main

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectMultipartConditionalRecovery(t *testing.T) {
	for _, cause := range []error{nil, objectstorage.ErrPreconditionFailed, objectstorage.ErrConditionalConflict, objectstorage.ErrConditionalNotFound, objectstorage.ErrUnavailable} {
		name := "success"
		if cause != nil {
			name = cause.Error()
		}
		t.Run(name, func(t *testing.T) {
			e := setup(t, api.PlanHobby)
			provider := &fakeObjectProvider{}
			e.s.WithObjectStorage(objectRegistry(t, provider, &fakeObjectProvider{}, "external"))
			setS3Flag(t, e, true)
			bucket := reserveRecoveryBucket(t, e)
			ctx := t.Context()
			if err := e.s.reconcileObjectBuckets(ctx, nil); err != nil {
				t.Fatal(err)
			}
			qualifyObjectAccounting(t, e, bucket.ID)
			uploads := e.s.store.(state.ObjectMultipartUploadStore)
			transfers := e.s.store.(state.ObjectMultipartTransferStore)
			u, err := uploads.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: bucket.AccountID, AppID: bucket.AppID, BucketID: bucket.ID, Key: "conditional", ExpiresAt: time.Now().Add(time.Hour)}, 100)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = uploads.ClaimObjectMultipartUpload(ctx, bucket.AccountID, bucket.AppID, bucket.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
				t.Fatal(err)
			}
			if err = uploads.ActivateObjectMultipartUpload(ctx, u.ID, "init", "provider"); err != nil {
				t.Fatal(err)
			}
			if err = transfers.BeginObjectMultipartPart(ctx, bucket.AccountID, bucket.ID, u.ID, "part", 1, 30, 100, e.s.objectStorage.Accounting); err != nil {
				t.Fatal(err)
			}
			if err = transfers.SettleObjectMultipartPart(ctx, bucket.AccountID, u.ID, 1, "part"); err != nil {
				t.Fatal(err)
			}
			u, err = uploads.GetObjectMultipartUpload(ctx, bucket.AccountID, bucket.AppID, bucket.ID, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			u.CompletionConditions = api.ObjectWriteConditions{IfMatch: `"old"`}
			parts := []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}
			if _, err = transfers.PrepareObjectMultipartCompletion(ctx, u, "complete", 30, parts, e.s.objectStorage.Accounting); err != nil {
				t.Fatal(err)
			}
			if err = uploads.RetryObjectMultipartUpload(ctx, u.ID, "complete", "temporary", time.Second); err != nil {
				t.Fatal(err)
			}
			time.Sleep(1100 * time.Millisecond)
			setS3Flag(t, e, false)
			provider.multipartErr = cause
			if err = e.s.reconcileObjectMultipartUploads(ctx, nil); err != nil {
				t.Fatal(err)
			}
			got, err := uploads.GetObjectMultipartUpload(ctx, bucket.AccountID, bucket.AppID, bucket.ID, u.ID)
			if err != nil || len(provider.multipartConditions) != 1 || provider.multipartConditions[0] != u.CompletionConditions {
				t.Fatal("recovery lost condition", got, provider.multipartConditions, err)
			}
			code := objectstorage.MultipartCompletionFailureCode(cause)
			if cause == nil {
				if got.State != state.ObjectMultipartCompleted || len(provider.multipartCompleted) != 1 {
					t.Fatal(got)
				}
			} else if code == "" {
				if got.State != state.ObjectMultipartCompletingConditional || got.LeaseToken != "" || got.CompletionErrorCode != "" {
					t.Fatal("transient failure became terminal", got)
				}
			} else {
				if got.State != state.ObjectMultipartAborting || got.CompletionErrorCode != code {
					t.Fatal("rejection still completing", got)
				}
				provider.multipartErr = nil
				if err = e.s.reconcileObjectMultipartUploads(ctx, nil); err != nil {
					t.Fatal(err)
				}
				got, err = uploads.GetObjectMultipartUpload(ctx, bucket.AccountID, bucket.AppID, bucket.ID, u.ID)
				usage, usageErr := e.store.ObjectUsage(ctx, bucket.AccountID, time.Now())
				if err != nil || usageErr != nil || got.State != state.ObjectMultipartAborted || got.CompletionErrorCode != code || len(provider.multipartConditions) != 1 || len(provider.multipartAborted) != 1 || usage.Buckets[0].MultipartBytes != 0 {
					t.Fatal("rejected completion not cleaned up", got, usage, err, usageErr)
				}
			}
		})
	}
}
