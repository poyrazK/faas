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

func TestObjectMultipartTransfersMem(t *testing.T) { multipartTransfersSuite(t, state.NewMemStore()) }
func TestObjectMultipartTransfersPG(t *testing.T)  { s, _ := pgStore(t); multipartTransfersSuite(t, s) }

func activeTrackedUpload(t *testing.T, st accountingStore, key string) (state.ObjectBucket, state.ObjectMultipartUpload) {
	t.Helper()
	ctx := context.Background()
	b, _ := seedAccounting(t, st)
	sessions := st.(state.ObjectMultipartUploadStore)
	u, err := sessions.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: key, ExpiresAt: time.Now().Add(time.Hour)}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = sessions.ActivateObjectMultipartUpload(ctx, u.ID, "init", "provider"); err != nil {
		t.Fatal(err)
	}
	u, err = sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return b, u
}

func multipartTransfersSuite(t *testing.T, st accountingStore) {
	ctx := context.Background()
	b, u := activeTrackedUpload(t, st, "tracked")
	p := accountingPolicy()
	sessions := st.(state.ObjectMultipartUploadStore)
	transfers := st.(state.ObjectMultipartTransferStore)
	begin := func(part int32, token string, size int64) error {
		return transfers.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, token, part, size, 100, p)
	}
	if err := begin(1, "first", 30); err != nil {
		t.Fatal(err)
	}
	if err := begin(1, "parallel", 40); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("same-part overlap: %v", err)
	}
	parts := []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: "etag"}}
	current, err := sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transfers.PrepareObjectMultipartCompletion(ctx, current, "complete", 30, parts, p); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("completed an in-flight part: %v", err)
	}
	if _, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "complete", state.ObjectMultipartCompleting, parts, false); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unfenced completion: %v", err)
	}
	if err = transfers.SettleObjectMultipartPart(ctx, uuid.NewString(), u.ID, 1, "first"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cross-account settlement: %v", err)
	}
	if err = transfers.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "wrong"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale settlement: %v", err)
	}
	if err = transfers.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "first"); err != nil {
		t.Fatal(err)
	}
	if err = begin(1, "replacement", 40); err != nil {
		t.Fatal(err)
	}
	if err = transfers.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "first"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old token settled replacement: %v", err)
	}
	if err = transfers.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "replacement"); err != nil {
		t.Fatal(err)
	}
	if _, err = transfers.PrepareObjectMultipartCompletion(ctx, current, "stale", 30, parts, p); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale provider listing accepted: %v", err)
	}
	// Failed preparation must not consume final-object capacity.
	snapshot, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := state.SummarizeObjectUsage(snapshot, p, time.Now()); got.CapacityBytes != 40 || got.CapacityKeys != 0 {
		t.Fatal(got)
	}
	if err = begin(2, "pending", 20); err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "abort", state.ObjectMultipartAborting, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = begin(3, "late", 10); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("write after abort fence: %v", err)
	}
	if ready, e := transfers.ObjectMultipartAbortReady(ctx, u.ID, "abort"); e != nil || ready {
		t.Fatalf("ready=%v err=%v", ready, e)
	}
	if err = transfers.FinishVerifiedObjectMultipartAbort(ctx, u.ID, "abort"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("released in-flight capacity: %v", err)
	}
	if err = transfers.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 2, "pending"); err != nil {
		t.Fatal(err)
	}
	if ready, e := transfers.ObjectMultipartAbortReady(ctx, u.ID, "abort"); e != nil || !ready {
		t.Fatalf("ready=%v err=%v", ready, e)
	}
	if err = transfers.FinishVerifiedObjectMultipartAbort(ctx, u.ID, "wrong"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale cleanup token: %v", err)
	}
	if err = transfers.FinishVerifiedObjectMultipartAbort(ctx, u.ID, "abort"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := state.SummarizeObjectUsage(snapshot, p, time.Now()); got.CapacityBytes != 0 {
		t.Fatal(got)
	}
	if err = st.AdmitObjectURL(ctx, b.AccountID, b.ID, "reused", 100, true, p); err != nil {
		t.Fatalf("capacity was not reusable: %v", err)
	}
}

func TestObjectMultipartPreparationAtomicMem(t *testing.T) {
	multipartPreparationAtomic(t, state.NewMemStore())
}
func TestObjectMultipartPreparationAtomicPG(t *testing.T) {
	s, _ := pgStore(t)
	multipartPreparationAtomic(t, s)
}
func multipartPreparationAtomic(t *testing.T, st accountingStore) {
	ctx := context.Background()
	b, u := activeTrackedUpload(t, st, "complete")
	p := accountingPolicy()
	sessions := st.(state.ObjectMultipartUploadStore)
	transfers := st.(state.ObjectMultipartTransferStore)
	if err := transfers.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "part", 1, 30, 100, p); err != nil {
		t.Fatal(err)
	}
	if err := transfers.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "part"); err != nil {
		t.Fatal(err)
	}
	u, err := sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	parts := []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: "etag"}}
	if _, err = transfers.PrepareObjectMultipartCompletion(ctx, u, "too-large", 101, parts, p); !errors.Is(err, state.ErrObjectCapacity) {
		t.Fatal(err)
	}
	after, err := sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil || after.State != state.ObjectMultipartActive || after.SizeBytes != 0 {
		t.Fatalf("%+v %v", after, err)
	}
	prepared, err := transfers.PrepareObjectMultipartCompletion(ctx, u, "complete", 30, parts, p)
	if err != nil || prepared.State != state.ObjectMultipartCompleting || prepared.SizeBytes != 30 || len(prepared.Parts) != 1 {
		t.Fatalf("%+v %v", prepared, err)
	}
	after, err = sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil || after.SizeBytes != 30 || after.Parts[0].ETag != "etag" {
		t.Fatalf("lost recovery intent: %+v %v", after, err)
	}
	if err = sessions.FinishObjectMultipartUpload(ctx, u.ID, "complete", state.ObjectMultipartCompleted); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := state.SummarizeObjectUsage(snapshot, p, time.Now()); got.CapacityBytes != 30 || got.CapacityKeys != 1 {
		t.Fatal(got)
	}
}

func TestObjectMultipartTransferConcurrencyMem(t *testing.T) {
	multipartTransferConcurrency(t, state.NewMemStore())
}
func TestObjectMultipartTransferConcurrencyPG(t *testing.T) {
	s, _ := pgStore(t)
	multipartTransferConcurrency(t, s)
}
func multipartTransferConcurrency(t *testing.T, st accountingStore) {
	ctx := context.Background()
	b, u := activeTrackedUpload(t, st, "parallel")
	transfers := st.(state.ObjectMultipartTransferStore)
	var wg sync.WaitGroup
	var winners atomic.Int32
	for part := int32(1); part <= 20; part++ {
		wg.Add(1)
		go func(part int32) {
			defer wg.Done()
			err := transfers.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, uuid.NewString(), part, 10, 100, accountingPolicy())
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, state.ErrObjectCapacity) {
				t.Error(err)
			}
		}(part)
	}
	wg.Wait()
	if winners.Load() != 10 {
		t.Fatalf("admitted %d parts, want 10", winners.Load())
	}
}

func TestObjectMultipartExpiredTransferAndLegacyPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	b, u := activeTrackedUpload(t, s, "crash")
	p := accountingPolicy()
	if err := s.AdmitObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, 1, 30, 100, p); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "legacy-retry", 1, 30, 100, p); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "lost-process", 2, 20, 100, p); err != nil {
		t.Fatal(err)
	}
	var seconds float64
	if err := pool.QueryRow(ctx, `SELECT extract(epoch FROM unsafe_until-clock_timestamp()) FROM object_storage_multipart_part_grants WHERE upload_id=$1 AND part_number=2`, u.ID).Scan(&seconds); err != nil {
		t.Fatal(err)
	}
	if seconds < 2150 || seconds > 2161 {
		t.Fatalf("unsafe transfer deadline: %f seconds", seconds)
	}
	if _, err := s.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "abort", state.ObjectMultipartAborting, nil, false); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.ObjectMultipartAbortReady(ctx, u.ID, "abort"); err != nil || ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE object_storage_multipart_part_grants SET unsafe_until=clock_timestamp()-interval '1 second' WHERE upload_id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	// Restarted workers recover durable fences; expiry does not release capacity.
	restarted := state.NewPgStore(pool)
	if ready, err := restarted.ObjectMultipartAbortReady(ctx, u.ID, "abort"); err != nil || !ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	snapshot, err := restarted.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := state.SummarizeObjectUsage(snapshot, p, time.Now()); got.CapacityBytes != 50 {
		t.Fatal(got)
	}
	if err = restarted.FinishVerifiedObjectMultipartAbort(ctx, u.ID, "abort"); err != nil {
		t.Fatal(err)
	}
	var pendingTokens int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_multipart_part_grants WHERE upload_id=$1 AND transfer_token IS NOT NULL`, u.ID).Scan(&pendingTokens); err != nil || pendingTokens != 0 {
		t.Fatalf("verified legacy cleanup retained transfer tokens: %d %v", pendingTokens, err)
	}
	snapshot, err = restarted.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := state.SummarizeObjectUsage(snapshot, p, time.Now()); got.CapacityBytes != 30 {
		t.Fatalf("untracked legacy capacity released: %+v", got)
	}
}
