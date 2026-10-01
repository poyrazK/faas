package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTrackedObjectUploadRecoveryPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	route, err := st.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Name: "files", MaxBytes: 100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	seed := func(ctx context.Context, idem string) state.ObjectUploadCompletion {
		c, _, e := st.BeginTrackedObjectUpload(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "owner", Key: idem, Bytes: 10, Status: "pending", IdempotencyKey: idem, RequestFingerprint: "fingerprint"}, accountingPolicy())
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	due := func(ctx context.Context, id string) {
		if _, e := pool.Exec(ctx, `UPDATE object_upload_completions SET recovery_retry_at=now()-interval '1 second',recovery_lease_until=NULL,recovery_token='' WHERE id=$1`, id); e != nil {
			t.Fatal(e)
		}
	}
	c := seed(ctx, "prepared")
	due(ctx, c.ID)
	c, err = st.ClaimTrackedObjectUploadRecovery(ctx, b.AccountID, b.ID, c.ID, "worker")
	if err != nil || c.Status != "failed" || c.ErrorCode != "preparation_expired" {
		t.Fatal(c, err)
	}
	if _, err = st.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, c.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("late dispatch", err)
	}
	c = seed(ctx, "dispatched")
	c, err = st.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	due(ctx, c.ID)
	c, err = st.ClaimTrackedObjectUploadRecovery(ctx, b.AccountID, b.ID, c.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimTrackedObjectUploadRecovery(ctx, b.AccountID, b.ID, c.ID, "duplicate"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("duplicate lease", err)
	}
	if err = st.RetryTrackedObjectUploadRecovery(ctx, c, "provider_write_uncertain"); err != nil {
		t.Fatal(err)
	}
	stale := c
	stale.Status = "completed"
	stale.ETag = "etag"
	stale.ErrorCode = ""
	if _, err = st.FinishTrackedObjectUploadRecovery(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale lease settled", err)
	}
	due(ctx, c.ID)
	c, err = st.ClaimTrackedObjectUploadRecovery(ctx, b.AccountID, b.ID, c.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	// Route removal preserves provider recovery data and its capacity journal.
	if err = st.DeleteObjectUploadRoute(ctx, b.AccountID, b.AppID, "files"); err != nil {
		t.Fatal(err)
	}
	var routeNull bool
	if err = pool.QueryRow(ctx, `SELECT route_id IS NULL FROM object_upload_completions WHERE id=$1`, c.ID).Scan(&routeNull); err != nil || !routeNull {
		t.Fatal(routeNull, err)
	}
	c.Status = "completed"
	c.ETag = "etag"
	c.ErrorCode = ""
	if _, err = st.FinishTrackedObjectUploadRecovery(ctx, c); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_write_admissions WHERE bucket_id=$1 AND state='pending'`, b.ID).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("journal did not settle atomically", pending, err)
	}
}
