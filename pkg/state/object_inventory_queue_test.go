// adr: 20261110151628

package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestObjectInventoryQueue covers the inventory sweep's queue: it holds what
// DueObjectInventories holds, leaves out the buckets the sweep already took,
// and marks a continuing listing long while a bucket never listed is not.
func TestObjectInventoryQueueMem(t *testing.T) { objectInventoryQueueSuite(t, state.NewMemStore()) }
func TestObjectInventoryQueuePG(t *testing.T) {
	st, _ := pgStore(t)
	objectInventoryQueueSuite(t, st)
}

func objectInventoryQueueSuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	fresh := seedRecoveryBucket(t, st)
	if _, err := st.ClaimObjectBucket(ctx, fresh.AccountID, fresh.AppID, fresh.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectBucket(ctx, fresh.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	listing, _ := seedAccounting(t, st)
	token := uuid.NewString()
	scan, err := st.ClaimObjectInventoryScan(ctx, listing.ID, token)
	if err != nil {
		t.Fatal(err)
	}
	scan.After, scan.Bytes, scan.Keys, scan.Pages = "k2", 20, 2, 2
	if err = st.CheckpointObjectInventoryScan(ctx, listing.ID, token, scan); err != nil {
		t.Fatal(err)
	}
	queue := func(skip []string) map[string]bool {
		t.Helper()
		rows, err := st.DueObjectInventoryQueue(ctx, 100, skip)
		if err != nil {
			t.Fatal(err)
		}
		long := map[string]bool{}
		for _, r := range rows {
			long[r.Bucket.ID] = r.Long
		}
		return long
	}
	got := queue(nil)
	if long, ok := got[fresh.ID]; !ok || long {
		t.Fatalf("never-listed bucket: due %v, long %v", ok, long)
	}
	if long, ok := got[listing.ID]; !ok || !long {
		t.Fatalf("continuing listing: due %v, long %v", ok, long)
	}
	got = queue([]string{fresh.ID})
	if _, ok := got[fresh.ID]; ok || !got[listing.ID] {
		t.Fatal("skip list", got)
	}
	buckets, err := st.DueObjectInventories(ctx, 100)
	if err != nil || len(buckets) != 2 {
		t.Fatal("DueObjectInventories lost a due bucket", buckets, err)
	}
	if _, err = st.DueObjectInventoryQueue(ctx, 0, nil); !errors.Is(err, state.ErrConflict) {
		t.Fatal("limit 0", err)
	}
}

// TestObjectInventoryQueueMarksLargeBucketsLongPG: a bucket whose last
// inventory counted more than ObjectStorageInventoryLongListingKeys keys is a
// long listing before its next listing starts.
func TestObjectInventoryQueueMarksLargeBucketsLongPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b := seedRecoveryBucket(t, st)
	if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if err := st.ClaimObjectInventory(ctx, b.ID, token); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectInventory(ctx, b.ID, token, api.ObjectStorageInventoryLongListingKeys+1, api.ObjectStorageInventoryLongListingKeys+1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE object_storage_bucket_usage SET attempt_at = now() - interval '10 minutes' WHERE bucket_id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := st.DueObjectInventoryQueue(ctx, 100, nil)
	if err != nil || len(rows) != 1 || rows[0].Bucket.ID != b.ID || !rows[0].Long {
		t.Fatal("large bucket not due as a long listing", rows, err)
	}
}
