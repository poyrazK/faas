package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
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
	c.RecoveryCursor = "private-cursor"
	c.RecoveryVersionsObserved = true
	oversized := c
	oversized.RecoveryCursor = strings.Repeat("x", api.ObjectUploadHistoryCursorMaxBytes+1)
	if err = st.RetryTrackedObjectUploadRecovery(ctx, oversized, "provider_write_uncertain"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unbounded cursor", err)
	}
	if err = st.RetryTrackedObjectUploadRecovery(ctx, c, "provider_write_uncertain"); err != nil {
		t.Fatal(err)
	}
	st = state.NewPgStore(pool)
	read, err := st.GetObjectUploadReceipt(ctx, b.AccountID, b.AppID, route.ID, "owner", c.ID)
	if err != nil || read.RecoveryCursor != c.RecoveryCursor || !read.RecoveryVersionsObserved {
		t.Fatal("lost recovery progress on restart", read, err)
	}
	stale := c
	stale.RecoveryCursor = "stale-worker-cursor"
	if err = st.RetryTrackedObjectUploadRecovery(ctx, stale, "provider_write_uncertain"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale cursor advance", err)
	}
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
	c.RecoveryVersionsObserved = false // A later worker cannot clear retained-version evidence.
	if read, err = st.FinishTrackedObjectUploadRecovery(ctx, c); err != nil || read.RecoveryCursor != "" || !read.RecoveryVersionsObserved {
		t.Fatal(err)
	}
	var pending int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_write_admissions WHERE bucket_id=$1 AND state='pending'`, b.ID).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("journal did not settle atomically", pending, err)
	}
	j, err := st.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = st.ClaimObjectCapacityReconciliation(ctx, j.ID, "capacity-worker")
	if err != nil || j.State != "blocked" || j.LastErrorCode != "version_accounting_required" {
		t.Fatal("current inventory could refund versions", j, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET recovery_versions_observed=false WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT recovery_versions_observed FROM object_upload_completions WHERE id=$1`, c.ID).Scan(&read.RecoveryVersionsObserved); err != nil || !read.RecoveryVersionsObserved {
		t.Fatal("database latch cleared", err)
	}
	// Simulate an older worker which does not know the new readiness predicate.
	if _, err = pool.Exec(ctx, `UPDATE object_storage_capacity_reconciliations SET state='scanning',lease_token='old-worker',lease_until=now()+interval '1 minute',finished_at=NULL WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_bucket_usage SET baseline_bytes=0,baseline_keys=0,granted_bytes=0,granted_keys=0 WHERE bucket_id=$1`, b.ID); err == nil {
		t.Fatal("older worker bypassed version accounting fence")
	}
}
