// adr: 590
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectUploadCaptureMem(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "journal", true: "retained request"}[legacy], func(t *testing.T) {
			m := state.NewMemStore()
			now := time.Now().UTC()
			m.SetClockForTest(func() time.Time { return now })
			uploadCaptureContract(t, m, legacy, func() { now = now.Add(time.Hour) })
		})
	}
}
func TestObjectUploadCapturePG(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "journal", true: "retained request"}[legacy], func(t *testing.T) {
			s, pool, ctx := pgStoreWithPool(t)
			uploadCaptureContract(t, s, legacy, func() {
				if _, err := pool.Exec(ctx, `UPDATE object_upload_completions SET recovery_retry_at=clock_timestamp()-interval '1 second',recovery_lease_until=NULL WHERE status='pending'`); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
func uploadCaptureContract(t *testing.T, st accountingStore, retainRequest bool, expire func()) {
	t.Helper()
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	routes := st.(state.ObjectUploadRouteStore)
	uploads := st.(state.ObjectTrackedUploadStore)
	fences := st.(state.ObjectBucketWriteFenceStore)
	route, err := routes.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, Name: "files", BucketID: b.ID, MaxBytes: 100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	c := state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "subject", Key: "original", Bytes: 10, Status: "pending", IdempotencyKey: "original", RequestFingerprint: "fingerprint"}
	requests := int64(0)
	if retainRequest {
		if _, err := fences.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest); err != nil {
			t.Fatal(err)
		}
		requests = 1
	}
	c, created, err := uploads.BeginTrackedObjectUpload(ctx, c, accountingPolicy())
	if err != nil || !created {
		t.Fatal(c, created, err)
	}
	receipt, err := st.(state.ObjectTrackedUploadMutationStore).ReadTrackedObjectUploadMutation(ctx, c)
	if err != nil || receipt.UploadID != c.ID || receipt.Bucket.PhysicalName != b.PhysicalName {
		t.Fatal("upload lost original placement", receipt, err)
	}
	if err := fences.FinishObjectBucketMutation(ctx, receipt); !errors.Is(err, state.ErrConflict) {
		t.Fatal("generic finish erased bound upload", err)
	}
	f, err := fences.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil || f.Uploads != 1 || f.Multipart != 0 || f.Requests != requests+1 {
		t.Fatal("prepared upload missing", f, err)
	}
	if replay, created, err := uploads.BeginTrackedObjectUpload(ctx, c, accountingPolicy()); err != nil || created || replay.ID != c.ID {
		t.Fatal("original replay blocked", replay, err)
	}
	fresh := c
	fresh.ID = uuid.NewString()
	fresh.IdempotencyKey = "new"
	fresh.Key = "new"
	if _, _, err := uploads.BeginTrackedObjectUpload(ctx, fresh, accountingPolicy()); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
		t.Fatal("new upload escaped", err)
	}
	c, err = uploads.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, c.ID)
	if err != nil {
		t.Fatal("admitted dispatch blocked", err)
	}
	expire()
	c, err = uploads.ClaimTrackedObjectUploadRecovery(ctx, b.AccountID, b.ID, c.ID, "recovery")
	if err != nil {
		t.Fatal(err)
	}
	if resumed, err := st.(state.ObjectTrackedUploadMutationStore).ReadTrackedObjectUploadMutation(ctx, c); err != nil || resumed.ID != receipt.ID {
		t.Fatal("held recovery lost bound receipt", resumed, err)
	}
	stale := c
	stale.RecoveryToken = "stale"
	if _, err := st.(state.ObjectTrackedUploadMutationStore).ReadTrackedObjectUploadMutation(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker reached original provider", err)
	}
	if err := uploads.RetryTrackedObjectUploadRecovery(ctx, c, "provider_write_uncertain"); err != nil {
		t.Fatal(err)
	}
	observed, err := fences.ReadObjectBucketWriteFence(ctx, b, f.Token)
	if err != nil || observed.Uploads != 1 {
		t.Fatal("uncertainty erased original", observed, err)
	}
	expire()
	c, err = uploads.ClaimTrackedObjectUploadRecovery(ctx, b.AccountID, b.ID, c.ID, "restarted")
	if err != nil {
		t.Fatal(err)
	}
	c.Status = "completed"
	c.ErrorCode = ""
	c.ETag = "authenticated-original-receipt"
	if _, err := uploads.FinishTrackedObjectUploadRecovery(ctx, c); err != nil {
		t.Fatal(err)
	}
	observed, err = fences.ReadObjectBucketWriteFence(ctx, b, f.Token)
	if err != nil || observed.Uploads != 0 || observed.Requests != requests {
		t.Fatal("journal settlement erased unrelated receipt", observed, err)
	}
	if _, _, err := uploads.BeginTrackedObjectUpload(ctx, fresh, accountingPolicy()); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
		t.Fatal("settlement reopened source", err)
	}
}

func TestObjectMultipartCaptureMem(t *testing.T) {
	m := state.NewMemStore()
	now := time.Now().UTC()
	m.SetClockForTest(func() time.Time { return now })
	multipartCaptureContract(t, m, func() { now = now.Add(2 * time.Hour) })
}
func TestObjectMultipartCapturePG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	multipartCaptureContract(t, s, func() {
		if _, err := pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET expires_at=clock_timestamp()-interval '1 second',retry_at=clock_timestamp()-interval '1 second',lease_until=NULL WHERE state='active'`); err != nil {
			t.Fatal(err)
		}
	})
}
func multipartCaptureContract(t *testing.T, st accountingStore, expire func()) {
	t.Helper()
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	sessions := st.(state.ObjectMultipartUploadStore)
	transfers := st.(state.ObjectMultipartTransferStore)
	fences := st.(state.ObjectBucketWriteFenceStore)
	input := state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "original", ExpiresAt: time.Now().Add(time.Hour)}
	u, err := sessions.ReserveObjectMultipartUpload(ctx, input, 100)
	if err != nil {
		t.Fatal(err)
	}
	f, err := fences.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil || f.Multipart != 1 || f.Uploads != 0 || f.Requests != 1 {
		t.Fatal("initiating upload missing", f, err)
	}
	if replay, err := sessions.ReserveObjectMultipartUpload(ctx, input, 100); err != nil || replay.ID != u.ID {
		t.Fatal("original session replay blocked", replay, err)
	}
	fresh := input
	fresh.ID = uuid.NewString()
	fresh.Key = "new"
	if _, err := sessions.ReserveObjectMultipartUpload(ctx, fresh, 100); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
		t.Fatal("new session escaped", err)
	}
	if _, err := sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := sessions.ActivateObjectMultipartUpload(ctx, u.ID, "init", "original-provider-upload"); err != nil {
		t.Fatal(err)
	}
	expire()
	observed, err := fences.ReadObjectBucketWriteFence(ctx, b, f.Token)
	if err != nil || observed.Multipart != 1 {
		t.Fatal("session expiry released capture", observed, err)
	}
	claimed, err := sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "abort", state.ObjectMultipartAborting, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	observed, err = fences.ReadObjectBucketWriteFence(ctx, b, f.Token)
	if err != nil || observed.Multipart != 1 {
		t.Fatal("abort claim erased custody", observed, err)
	}
	receipt, err := st.(state.ObjectMultipartMutationStore).ReadObjectMultipartMutation(ctx, claimed)
	if err != nil || receipt.MultipartUploadID != u.ID || receipt.Bucket.PhysicalName != b.PhysicalName {
		t.Fatal("held abort lost original placement", receipt, err)
	}
	if err := fences.FinishObjectBucketMutation(ctx, receipt); !errors.Is(err, state.ErrConflict) {
		t.Fatal("generic ACK retired session", err)
	}
	for _, change := range []func(*state.ObjectMultipartUpload){func(u *state.ObjectMultipartUpload) { u.ID = uuid.NewString() }, func(u *state.ObjectMultipartUpload) { u.AccountID = uuid.NewString() }, func(u *state.ObjectMultipartUpload) { u.LeaseToken = "stale" }, func(u *state.ObjectMultipartUpload) { u.ProviderUploadID = "changed" }, func(u *state.ObjectMultipartUpload) { u.Key = "changed" }} {
		stale := claimed
		change(&stale)
		if _, err := st.(state.ObjectMultipartMutationStore).ReadObjectMultipartMutation(ctx, stale); !errors.Is(err, state.ErrConflict) {
			t.Fatal("changed authority resumed", err)
		}
	}
	ready, err := transfers.ObjectMultipartAbortReady(ctx, u.ID, "abort")
	if err != nil || !ready {
		t.Fatal(ready, err)
	}
	if err := transfers.FinishVerifiedObjectMultipartAbort(ctx, u.ID, "abort"); err != nil {
		t.Fatal(err)
	}
	observed, err = fences.ReadObjectBucketWriteFence(ctx, b, f.Token)
	if err != nil || observed.Multipart != 0 || observed.Requests != 0 {
		t.Fatal("verified abort still counted", observed, err)
	}
	if _, err := sessions.ReserveObjectMultipartUpload(ctx, fresh, 100); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
		t.Fatal("abort reopened source", err)
	}
}

func TestObjectUploadCaptureLegacyFailureMem(t *testing.T) {
	legacyUploadCaptureContract(t, state.NewMemStore())
}

func TestObjectUploadCaptureLegacyFailurePG(t *testing.T) {
	s, _, _ := pgStoreWithPool(t)
	legacyUploadCaptureContract(t, s)
}

func legacyUploadCaptureContract(t *testing.T, st accountingStore) {
	t.Helper()
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	routes := st.(state.ObjectUploadRouteStore)
	route, err := routes.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, Name: "legacy", BucketID: b.ID, MaxBytes: 100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	c := state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "subject", Key: "uncertain", Bytes: 10, Status: "failed", ErrorCode: "provider_write_failed"}
	if _, err := routes.RecordObjectUploadCompletion(ctx, c); err != nil {
		t.Fatal(err)
	}
	fences := st.(state.ObjectBucketWriteFenceStore)
	f, err := fences.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil || f.Uploads != 1 {
		t.Fatal("legacy uncertainty lost", f, err)
	}
	c.ID = uuid.NewString()
	if _, err := routes.RecordObjectUploadCompletion(ctx, c); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
		t.Fatal("legacy admission escaped hold", err)
	}
}
