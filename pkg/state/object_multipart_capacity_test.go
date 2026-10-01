package state_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectMultipartCapacityMem(t *testing.T) { multipartCapacitySuite(t, state.NewMemStore()) }
func TestObjectMultipartCapacityPG(t *testing.T)  { s, _ := pgStore(t); multipartCapacitySuite(t, s) }
func multipartCapacitySuite(t *testing.T, st accountingStore) {
	ctx := context.Background()
	b, _ := seedAccounting(t, st)
	p := accountingPolicy()
	sessions := st.(state.ObjectMultipartUploadStore)
	capacity := st.(state.ObjectMultipartCapacityStore)
	create := func(key string) state.ObjectMultipartUpload {
		t.Helper()
		u, e := sessions.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: key, ExpiresAt: time.Now().Add(time.Hour)}, 100)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); e != nil {
			t.Fatal(e)
		}
		if e = sessions.ActivateObjectMultipartUpload(ctx, u.ID, "init", "provider-"+u.ID); e != nil {
			t.Fatal(e)
		}
		return u
	}
	u := create("object")
	reserve := func(part int32, size int64) error {
		return capacity.AdmitObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, part, size, 100, p)
	}
	if e := reserve(1, 10); e != nil {
		t.Fatal(e)
	}
	for _, size := range []int64{10, 5, 20} {
		if e := reserve(1, size); e != nil {
			t.Fatal(e)
		}
	}
	snapshot, _ := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if got := state.SummarizeObjectUsage(snapshot, p, time.Now()); got.CapacityBytes != 20 || got.CapacityKeys != 0 {
		t.Fatal(got)
	}
	if e := capacity.AdmitObjectMultipartPart(ctx, uuid.NewString(), b.ID, u.ID, 2, 10, 100, p); e == nil {
		t.Fatal("cross-account reservation succeeded")
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for part := int32(2); part <= 21; part++ {
		wg.Add(1)
		go func(part int32) {
			defer wg.Done()
			e := reserve(part, 10)
			if e == nil {
				winners.Add(1)
			} else if !errors.Is(e, state.ErrObjectCapacity) {
				t.Error(e)
			}
		}(part)
	}
	wg.Wait()
	if winners.Load() != 8 {
		t.Fatalf("admitted %d parts, want 8", winners.Load())
	}
	if e := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "other", 1, true, p); !errors.Is(e, state.ErrObjectCapacity) {
		t.Fatalf("incomplete parts bypassed quota: %v", e)
	}
	// Replace this session's reservation in the admission calculation, then keep
	// both grants until the provider completion is durably confirmed.
	if e := capacity.AdmitObjectMultipartCompletion(ctx, b.AccountID, b.ID, u.ID, u.Key, 100, p); e != nil {
		t.Fatal(e)
	}
	snapshot, _ = st.ObjectUsage(ctx, b.AccountID, time.Now())
	if got := state.SummarizeObjectUsage(snapshot, p, time.Now()); got.CapacityBytes != 200 {
		t.Fatal(got)
	}
	parts := []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: "etag"}}
	if _, e := sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "complete", state.ObjectMultipartCompleting, parts, false); e != nil {
		t.Fatal(e)
	}
	if e := reserve(2, 1); !errors.Is(e, state.ErrConflict) {
		t.Fatalf("part admitted while completing: %v", e)
	}
	if e := sessions.SetObjectMultipartUploadSize(ctx, u.ID, "complete", 100); e != nil {
		t.Fatal(e)
	}
	if e := sessions.FinishObjectMultipartUpload(ctx, u.ID, "complete", state.ObjectMultipartCompleted); e != nil {
		t.Fatal(e)
	}
	snapshot, _ = st.ObjectUsage(ctx, b.AccountID, time.Now())
	if got := state.SummarizeObjectUsage(snapshot, p, time.Now()); got.CapacityBytes != 100 || got.CapacityKeys != 1 {
		t.Fatal(got)
	}
	// Multipart listing is independent of terminal history and follows key markers.
	alpha, beta := create("alpha"), create("beta")
	lister := st.(state.ObjectS3MultipartLister)
	rows, e := lister.ListObjectS3MultipartUploads(ctx, b.AccountID, b.AppID, b.ID, "", "", "", 2)
	if e != nil || len(rows) != 2 || rows[0].ID != alpha.ID || rows[1].ID != beta.ID {
		t.Fatalf("rows=%+v err=%v", rows, e)
	}
	rows, e = lister.ListObjectS3MultipartUploads(ctx, b.AccountID, b.AppID, b.ID, "", alpha.Key, alpha.ID, 2)
	if e != nil || len(rows) != 1 || rows[0].ID != beta.ID {
		t.Fatalf("marker rows=%+v err=%v", rows, e)
	}
	// Part retries and cross-object admission cannot remove the final grant.
	if e := reserve(1, 1); !errors.Is(e, state.ErrConflict) {
		t.Fatal(e)
	}
}
func TestObjectMultipartAbortRetainsCapacityMem(t *testing.T) {
	multipartAbortCapacity(t, state.NewMemStore())
}
func TestObjectMultipartAbortRetainsCapacityPG(t *testing.T) {
	s, _ := pgStore(t)
	multipartAbortCapacity(t, s)
}
func multipartAbortCapacity(t *testing.T, st accountingStore) {
	ctx := context.Background()
	b, _ := seedAccounting(t, st)
	p := accountingPolicy()
	sessions := st.(state.ObjectMultipartUploadStore)
	capacity := st.(state.ObjectMultipartCapacityStore)
	u, e := sessions.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "abort", ExpiresAt: time.Now().Add(time.Hour)}, 100)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); e != nil {
		t.Fatal(e)
	}
	if e = sessions.ActivateObjectMultipartUpload(ctx, u.ID, "init", "provider"); e != nil {
		t.Fatal(e)
	}
	if e = capacity.AdmitObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, 1, 30, 100, p); e != nil {
		t.Fatal(e)
	}
	if _, e = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "abort", state.ObjectMultipartAborting, nil, false); e != nil {
		t.Fatal(e)
	}
	if e = sessions.FinishObjectMultipartUpload(ctx, u.ID, "abort", state.ObjectMultipartAborted); e != nil {
		t.Fatal(e)
	}
	snapshot, _ := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if got := state.SummarizeObjectUsage(snapshot, p, time.Now()); got.CapacityBytes != 30 {
		t.Fatal(got)
	}
}
