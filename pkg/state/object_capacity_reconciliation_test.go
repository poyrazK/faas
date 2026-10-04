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

func TestObjectCapacityReconciliationMem(t *testing.T) { objectCapacitySuite(t, state.NewMemStore()) }
func TestObjectCapacityReconciliationPG(t *testing.T)  { s, _ := pgStore(t); objectCapacitySuite(t, s) }
func objectCapacitySuite(t *testing.T, st accountingStore) {
	cap := st.(state.ObjectCapacityStore)
	p := accountingPolicy()

	t.Run("new bucket baseline", func(t *testing.T) {
		ctx := context.Background()
		b := seedRecoveryBucket(t, st)
		if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "create", "provisioning"); err != nil {
			t.Fatal(err)
		}
		if err := st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
			t.Fatal(err)
		}
		j, err := cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		j, err = cap.ClaimObjectCapacityReconciliation(ctx, j.ID, "baseline")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = cap.FinishObjectCapacityReconciliation(ctx, j.ID, "baseline", 3, 1); err != nil {
			t.Fatal(err)
		}
		usage, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
		if err != nil || len(usage.Buckets) != 1 || usage.Buckets[0].BaselineBytes != 3 || usage.Buckets[0].ObservedAt.IsZero() {
			t.Fatal(usage, err)
		}
	})
	t.Run("tracked writes cancellation and rebase", func(t *testing.T) {
		ctx := context.Background()
		b, _ := seedAccounting(t, st)
		one, two := uuid.NewString(), uuid.NewString()
		for i, token := range []string{one, two} {
			// A replacement is admitted after the previous receipt settles.
			if i > 0 {
				if err := cap.SettleObjectWrite(ctx, b.AccountID, b.ID, one); err != nil {
					t.Fatal(err)
				}
			}
			if err := cap.BeginObjectWrite(ctx, b.AccountID, b.ID, token, "key", int64(10+10*i), p); err != nil {
				t.Fatal(err)
			}
		}
		if err := cap.SettleObjectWrite(ctx, uuid.NewString(), b.ID, one); !errors.Is(err, state.ErrNotFound) {
			t.Fatal(err)
		}
		if err := cap.SettleObjectWrite(ctx, b.AccountID, b.ID, one); err != nil {
			t.Fatal(err)
		}
		inventory := uuid.NewString()
		if err := st.ClaimObjectInventory(ctx, b.ID, inventory); err != nil {
			t.Fatal(err)
		}
		j, err := cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		duplicate, err := cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
		if err != nil || duplicate.ID != j.ID {
			t.Fatal(duplicate, err)
		}
		if _, err = cap.GetObjectCapacityReconciliation(ctx, uuid.NewString(), b.ID, j.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("cross-account read", err)
		}
		for _, err := range []error{
			cap.BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "new", 1, p),
			st.AdmitObjectURL(ctx, b.AccountID, b.ID, "legacy", 1, true, p),
			st.FinishObjectInventory(ctx, b.ID, inventory, 0, 0),
		} {
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("write/inventory bypassed fence", err)
			}
		}
		if _, err = st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "delete", "deleting"); !errors.Is(err, state.ErrConflict) {
			t.Fatal("deletion bypassed fence", err)
		}
		if err = st.AdmitObjectURL(ctx, b.AccountID, b.ID, "read", 0, false, p); err != nil {
			t.Fatal("fenced reads", err)
		}
		if _, err = st.(state.ObjectMultipartUploadStore).ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "mpu", ExpiresAt: time.Now().Add(time.Hour)}, 10); !errors.Is(err, state.ErrConflict) {
			t.Fatal("multipart bypassed fence", err)
		}
		j, err = cap.ClaimObjectCapacityReconciliation(ctx, j.ID, "worker")
		if err != nil || j.State != "waiting" || j.PendingWrites != 1 || j.LastErrorCode != "unsettled_writes" {
			t.Fatal(j, err)
		}
		if _, err = cap.FinishObjectCapacityReconciliation(ctx, j.ID, "worker", 0, 0); !errors.Is(err, state.ErrConflict) {
			t.Fatal("pending write refunded", err)
		}
		if _, err = cap.CancelObjectCapacityReconciliation(ctx, b.AccountID, b.ID, j.ID); err != nil {
			t.Fatal(err)
		}
		if err = cap.SettleObjectWrite(ctx, b.AccountID, b.ID, two); err != nil {
			t.Fatal(err)
		}
		j, err = cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		var winners atomic.Int32
		var wg sync.WaitGroup
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := cap.ClaimObjectCapacityReconciliation(ctx, j.ID, "winner")
				if e == nil {
					winners.Add(1)
				} else if !errors.Is(e, state.ErrConflict) {
					t.Error(e)
				}
			}()
		}
		wg.Wait()
		if winners.Load() != 1 {
			t.Fatal("concurrent scan owners", winners.Load())
		}
		if _, err = cap.FinishObjectCapacityReconciliation(ctx, j.ID, "stale", 0, 0); !errors.Is(err, state.ErrConflict) {
			t.Fatal(err)
		}
		if err = cap.RetryObjectCapacityReconciliation(ctx, j.ID, "winner"); err != nil {
			t.Fatal(err)
		}
		snapshot, _ := st.ObjectUsage(ctx, b.AccountID, time.Now())
		if state.SummarizeObjectUsage(snapshot, p, time.Now()).CapacityBytes != 20 {
			t.Fatal("failed scan refunded quota")
		}
		if _, err = cap.CancelObjectCapacityReconciliation(ctx, b.AccountID, b.ID, j.ID); err != nil {
			t.Fatal(err)
		}
		j, err = cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		j, err = cap.ClaimObjectCapacityReconciliation(ctx, j.ID, "finish")
		if err != nil || j.State != "scanning" {
			t.Fatal(j, err)
		}
		before, _ := st.ObjectUsage(ctx, b.AccountID, time.Now())
		j, err = cap.FinishObjectCapacityReconciliation(ctx, j.ID, "finish", 0, 0)
		if err != nil || j.State != "completed" || j.ReclaimedBytes != 20 || j.ReclaimedKeys != 1 {
			t.Fatal(j, err)
		}
		after, _ := st.ObjectUsage(ctx, b.AccountID, time.Now())
		if after.Authorizations != before.Authorizations || len(after.Reports) != len(before.Reports) || state.SummarizeObjectUsage(after, p, time.Now()).CapacityBytes != 0 {
			t.Fatal("rebase changed billing or retained capacity", after)
		}
		if err = st.FinishObjectInventory(ctx, b.ID, inventory, 99, 1); !errors.Is(err, state.ErrConflict) {
			t.Fatal("old inventory undid rebase", err)
		}
		if err = cap.BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "full", 100, p); err != nil {
			t.Fatal("capacity was not reusable", err)
		}
	})
	for _, scenario := range []string{"legacy cannot upgrade", "direct write downgrades tracked grant"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			b, _ := seedAccounting(t, st)
			token := uuid.NewString()
			if scenario == "legacy cannot upgrade" {
				if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "key", 10, true, p); err != nil {
					t.Fatal(err)
				}
			}
			if err := cap.BeginObjectWrite(ctx, b.AccountID, b.ID, token, "key", 20, p); err != nil {
				t.Fatal(err)
			}
			if err := cap.SettleObjectWrite(ctx, b.AccountID, b.ID, token); err != nil {
				t.Fatal(err)
			}
			if scenario == "direct write downgrades tracked grant" {
				if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "key", 1, true, p); err != nil {
					t.Fatal(err)
				}
			}
			j, err := cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
			if err != nil {
				t.Fatal(err)
			}
			j, err = cap.ClaimObjectCapacityReconciliation(ctx, j.ID, "blocked")
			if err != nil || j.State != "blocked" || j.LastErrorCode != "untracked_writes" || j.AfterBytes != 20 || j.ReclaimedBytes != 0 {
				t.Fatal(j, err)
			}
			if err := cap.BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "after", 1, p); err != nil {
				t.Fatal("blocked job retained write fence", err)
			}
		})
	}
	t.Run("public multipart completion is reclaimable", func(t *testing.T) {
		ctx := context.Background()
		b, _ := seedAccounting(t, st)
		sessions := st.(state.ObjectMultipartUploadStore)
		transfers := st.(state.ObjectMultipartTransferStore)
		u, err := sessions.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "multipart", ExpiresAt: time.Now().Add(time.Hour)}, 10)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatal("live multipart allowed reconciliation", err)
		}
		if _, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
			t.Fatal(err)
		}
		if err = sessions.ActivateObjectMultipartUpload(ctx, u.ID, "init", "provider"); err != nil {
			t.Fatal(err)
		}
		if err = transfers.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "part", 1, 10, 100, p); err != nil {
			t.Fatal(err)
		}
		if err = transfers.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "part"); err != nil {
			t.Fatal(err)
		}
		u, err = sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		u, err = transfers.PrepareObjectMultipartCompletion(ctx, u, "complete", 10, []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: "etag"}}, p)
		if err != nil {
			t.Fatal(err)
		}
		if err = sessions.FinishObjectMultipartUpload(ctx, u.ID, "complete", state.ObjectMultipartCompleted); err != nil {
			t.Fatal(err)
		}
		j, err := cap.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		j, err = cap.ClaimObjectCapacityReconciliation(ctx, j.ID, "scan")
		if err != nil || j.State != "scanning" {
			t.Fatal(j, err)
		}
		j, err = cap.FinishObjectCapacityReconciliation(ctx, j.ID, "scan", 4, 1)
		if err != nil || j.ReclaimedBytes != 6 || j.AfterBytes != 4 {
			t.Fatal(j, err)
		}
	})
}
