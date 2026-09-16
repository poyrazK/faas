package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemStoreObjectMultipartRetryListingAndDue(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	m := NewMemStore()
	m.objectMultipartUploads = map[string]ObjectMultipartUpload{
		"upload-1": {
			ID: "upload-1", AccountID: "account-1", AppID: "app-1", BucketID: "bucket-1", Key: "one.bin",
			State: ObjectMultipartInitiating, LeaseToken: "lease-1", LeaseUntil: now.Add(time.Minute), RetryAt: now,
		},
		"upload-2": {
			ID: "upload-2", AccountID: "account-1", AppID: "app-1", BucketID: "bucket-1", Key: "two.bin",
			State: ObjectMultipartCompleted, RetryAt: now,
		},
	}

	if err := m.RetryObjectMultipartUpload(ctx, "upload-1", "lease-1", "provider-timeout", time.Second); err != nil {
		t.Fatalf("RetryObjectMultipartUpload: %v", err)
	}
	updated, err := m.GetObjectMultipartUpload(ctx, "account-1", "app-1", "bucket-1", "upload-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.LeaseToken != "" || updated.LastErrorCode != "provider-timeout" || !updated.RetryAt.After(now) {
		t.Fatalf("retried upload = %#v", updated)
	}
	if err := m.RetryObjectMultipartUpload(ctx, "upload-1", "", "provider-timeout", time.Second); !errors.Is(err, ErrConflict) {
		t.Fatalf("empty lease token error = %v", err)
	}
	if err := m.RetryObjectMultipartUpload(ctx, "upload-1", "lease-1", "", time.Second); !errors.Is(err, ErrConflict) {
		t.Fatalf("empty retry code error = %v", err)
	}

	listed, next, err := m.ListObjectMultipartUploads(ctx, "account-1", "app-1", "bucket-1", 1, "")
	if err != nil || len(listed) != 1 || listed[0].ID != "upload-1" || next != "upload-1" {
		t.Fatalf("ListObjectMultipartUploads = %#v, next=%q, err=%v", listed, next, err)
	}
	if _, _, err := m.ListObjectMultipartUploads(ctx, "account-1", "app-1", "bucket-1", 0, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid list limit error = %v", err)
	}
	if got, _, err := m.ListObjectMultipartUploads(ctx, "account-1", "app-1", "bucket-1", 10, "upload-1"); err != nil || len(got) != 1 || got[0].ID != "upload-2" {
		t.Fatalf("cursor-filtered list = %#v, err=%v", got, err)
	}

	m.mu.Lock()
	retried := m.objectMultipartUploads["upload-1"]
	retried.RetryAt = time.Now().UTC().Add(-time.Second)
	m.objectMultipartUploads["upload-1"] = retried
	m.mu.Unlock()
	due, err := m.DueObjectMultipartUploads(ctx, 10)
	if err != nil || len(due) != 1 || due[0].ID != "upload-1" {
		t.Fatalf("DueObjectMultipartUploads = %#v, err=%v", due, err)
	}
	if _, err := m.DueObjectMultipartUploads(ctx, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid due limit error = %v", err)
	}
}
