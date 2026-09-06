package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectMultipartUploadStoreMem(t *testing.T) {
	objectMultipartUploadStoreSuite(t, state.NewMemStore())
}

func TestObjectMultipartUploadStorePG(t *testing.T) {
	store, _ := pgStore(t)
	objectMultipartUploadStoreSuite(t, store)
}

func objectMultipartUploadStoreSuite(t *testing.T, base state.Store) {
	t.Helper()
	ctx := context.Background()
	buckets := base.(state.ObjectBucketStore)
	uploads := base.(state.ObjectMultipartUploadStore)
	account, err := base.CreateAccount(ctx, "multipart-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := base.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "multipart", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	bucketID := uuid.NewString()
	bucket, err := buckets.ReserveObjectBucket(ctx, state.ObjectBucket{
		ID: bucketID, AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1",
		BackendID: "external", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + strings.ReplaceAll(bucketID, "-", ""),
	}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = buckets.ClaimObjectBucket(ctx, account.ID, app.ID, bucket.ID, "ready", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = buckets.FinishObjectBucket(ctx, bucket.ID, "ready", "ready"); err != nil {
		t.Fatal(err)
	}

	request := state.ObjectMultipartUpload{
		ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, BucketID: bucket.ID, Key: "large/file.bin",
		SizeBytes: 130 << 20, PartSizeBytes: 64 << 20, PartCount: 3, ContentType: "application/octet-stream",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	upload, err := uploads.ReserveObjectMultipartUpload(ctx, request, 2)
	if err != nil || upload.State != state.ObjectMultipartInitiating {
		t.Fatal(upload, err)
	}
	retry := request
	retry.ID = uuid.NewString()
	retryUpload, err := uploads.ReserveObjectMultipartUpload(ctx, retry, 2)
	if err != nil || retryUpload.ID != upload.ID {
		t.Fatal("create retry changed session", retryUpload, err)
	}
	mismatch := retry
	mismatch.SizeBytes++
	if _, err = uploads.ReserveObjectMultipartUpload(ctx, mismatch, 2); !errors.Is(err, state.ErrConflict) {
		t.Fatal("mismatched retry accepted", err)
	}
	if _, err = uploads.GetObjectMultipartUpload(ctx, uuid.NewString(), app.ID, bucket.ID, upload.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account read", err)
	}
	second := request
	second.ID, second.Key = uuid.NewString(), "second.bin"
	if _, err = uploads.ReserveObjectMultipartUpload(ctx, second, 1); !errors.Is(err, state.ErrConflict) {
		t.Fatal("active-session limit bypassed", err)
	}
	claimed, err := uploads.ClaimObjectMultipartUpload(ctx, account.ID, app.ID, bucket.ID, upload.ID, "init", state.ObjectMultipartInitiating, nil, false)
	if err != nil || claimed.LeaseToken != "init" {
		t.Fatal(claimed, err)
	}
	if _, err = uploads.ClaimObjectMultipartUpload(ctx, account.ID, app.ID, bucket.ID, upload.ID, "racer", state.ObjectMultipartInitiating, nil, false); !errors.Is(err, state.ErrConflict) {
		t.Fatal("double claim", err)
	}
	if err = uploads.ActivateObjectMultipartUpload(ctx, upload.ID, "wrong", "provider-id"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale activation", err)
	}
	if err = uploads.ActivateObjectMultipartUpload(ctx, upload.ID, "init", "provider-id"); err != nil {
		t.Fatal(err)
	}
	if _, err = buckets.ClaimObjectBucket(ctx, account.ID, app.ID, bucket.ID, "delete", "deleting"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("bucket deletion orphaned multipart upload", err)
	}
	parts := []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: "one"}, {PartNumber: 2, ETag: "two"}, {PartNumber: 3, ETag: "three"}}
	claimed, err = uploads.ClaimObjectMultipartUpload(ctx, account.ID, app.ID, bucket.ID, upload.ID, "complete", state.ObjectMultipartCompleting, parts, false)
	if err != nil || len(claimed.Parts) != 3 {
		t.Fatal(claimed, err)
	}
	if err = uploads.FinishObjectMultipartUpload(ctx, upload.ID, "complete", state.ObjectMultipartCompleted); err != nil {
		t.Fatal(err)
	}
	completed, err := uploads.GetObjectMultipartUpload(ctx, account.ID, app.ID, bucket.ID, upload.ID)
	if err != nil || completed.State != state.ObjectMultipartCompleted {
		t.Fatal(completed, err)
	}

	expiredRequest := request
	expiredRequest.ID, expiredRequest.Key, expiredRequest.ExpiresAt = uuid.NewString(), "expired.bin", time.Now().Add(-time.Minute)
	expired, err := uploads.ReserveObjectMultipartUpload(ctx, expiredRequest, 2)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = uploads.ClaimObjectMultipartUpload(ctx, account.ID, app.ID, bucket.ID, expired.ID, "init-expired", state.ObjectMultipartInitiating, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = uploads.ActivateObjectMultipartUpload(ctx, expired.ID, "init-expired", "provider-expired"); err != nil {
		t.Fatal(err)
	}
	due, err := uploads.DueObjectMultipartUploads(ctx, 10)
	if err != nil || len(due) != 1 || due[0].ID != expired.ID {
		t.Fatal("expired session not due", due, err)
	}
	claimed, err = uploads.ClaimObjectMultipartUpload(ctx, account.ID, app.ID, bucket.ID, expired.ID, "abort", state.ObjectMultipartAborting, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = uploads.FinishObjectMultipartUpload(ctx, expired.ID, "abort", state.ObjectMultipartAborted); err != nil {
		t.Fatal(err)
	}
	if _, err = buckets.ClaimObjectBucket(ctx, account.ID, app.ID, bucket.ID, "delete", "deleting"); err != nil {
		t.Fatal("terminal uploads blocked bucket deletion", err)
	}
}
