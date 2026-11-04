package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreSnapshotRestorePressureLeaseReleaseAndExpiry(t *testing.T) {
	t.Parallel()
	testSnapshotRestorePressureLeases(t, state.NewMemStore(), "node-a")
}

// testSnapshotRestorePressureLeases checks the lock-free lease contract (ADR
// 20261103195512): leases count per node, the caller's own are left out,
// release removes them and expiry ends them. Writes and deletes may land in
// the background, so reads poll.
func testSnapshotRestorePressureLeases(t *testing.T, store state.SnapshotRestorePressureLeases, nodeID string) {
	t.Helper()
	ctx := context.Background()
	waitCounts := func(exclude []string, want int) {
		t.Helper()
		var counts map[string]int
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			var err error
			counts, err = store.ActiveSnapshotRestoreCounts(ctx, exclude)
			if err != nil {
				t.Fatal(err)
			}
			if counts[nodeID] == want && len(counts) == min(want, 1) {
				return
			}
		}
		t.Fatalf("active restore counts (exclude %v) = %#v, want %s:%d", exclude, counts, nodeID, want)
	}

	first, second := uuid.NewString(), uuid.NewString()
	releaseFirst, err := store.ReserveSnapshotRestore(ctx, first, nodeID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	releaseSecond, err := store.ReserveSnapshotRestore(ctx, second, nodeID, time.Minute)
	if err != nil {
		releaseFirst()
		t.Fatal(err)
	}
	waitCounts(nil, 2)
	waitCounts([]string{first}, 1)
	waitCounts([]string{first, second}, 0)

	releaseFirst()
	releaseFirst() // a deferred cleanup after the explicit release is safe
	waitCounts(nil, 1)
	releaseSecond()
	waitCounts(nil, 0)

	if _, err := store.ReserveSnapshotRestore(ctx, uuid.NewString(), nodeID, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	waitCounts(nil, 0)

	if _, err := store.ReserveSnapshotRestore(ctx, "", nodeID, time.Minute); err == nil {
		t.Fatal("reserve without a lease id succeeded")
	}
}
