package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReceiptRejectionRecoveryMem(t *testing.T) {
	st := state.NewMemStore()
	now := time.Now().UTC()
	st.SetClockForTest(func() time.Time { return now })
	receiptRejectionRecoveryContract(t, st, func(string) { now = now.Add(time.Hour) })
}

func TestReceiptRejectionRecoveryPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	receiptRejectionRecoveryContract(t, st, func(id string) {
		if _, err := pool.Exec(ctx, `UPDATE object_upload_completions SET recovery_retry_at=now()-interval '1 second',recovery_lease_until=NULL,recovery_token='' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	})
}

func receiptRejectionRecoveryContract(t *testing.T, st accountingStore, expire func(string)) {
	t.Helper()
	ctx := t.Context()
	bucket, _ := seedAccounting(t, st)
	uploads := st.(state.ObjectTrackedGatewayUploadStore)
	fences := st.(state.ObjectBucketWriteFenceStore)
	c, err := uploads.BeginTrackedGatewayUpload(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: bucket.AccountID, AppID: bucket.AppID, BucketID: bucket.ID, SubjectID: "owner", Key: "key", Bytes: 10, Status: "pending"}, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	c, err = uploads.DispatchTrackedObjectUpload(ctx, c.AccountID, c.BucketID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := fences.AcquireObjectBucketWriteFence(ctx, bucket, uuid.NewString())
	if err != nil || fence.Uploads != 1 {
		t.Fatal(fence, err)
	}
	expire(c.ID)
	c, err = uploads.ClaimTrackedObjectUploadRecovery(ctx, c.AccountID, c.BucketID, c.ID, "receipt-worker")
	if err != nil {
		t.Fatal(err)
	}
	c.Status = "failed"
	for _, code := range []string{"preparation_expired", "usage_unavailable", "dispatch_failed"} {
		c.ErrorCode = code
		if _, err = uploads.FinishTrackedObjectUploadRecovery(ctx, c); !errors.Is(err, state.ErrConflict) {
			t.Fatal("recovery settled without a provider rejection", code, err)
		}
	}
	c.ErrorCode = "provider_write_rejected"
	stale := c
	stale.RecoveryToken = "stale-worker"
	if _, err = uploads.FinishTrackedObjectUploadRecovery(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker settled admission", err)
	}
	done, err := uploads.FinishTrackedObjectUploadRecovery(ctx, c)
	if err != nil || done.Status != "failed" || done.WritePhase != state.ObjectUploadSettled {
		t.Fatal(done, err)
	}
	fence, err = fences.ReadObjectBucketWriteFence(ctx, bucket, fence.Token)
	if err != nil || fence.Uploads != 0 || fence.Requests != 0 {
		t.Fatal("terminal rejection retained or erased unrelated admission", fence, err)
	}
	c.Status, c.ErrorCode, c.ETag = "completed", "", "late-proof"
	if _, err = uploads.FinishTrackedObjectUploadRecovery(ctx, c); !errors.Is(err, state.ErrConflict) {
		t.Fatal("late proof rewrote a terminal rejection", err)
	}
}
