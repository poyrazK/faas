package state

import (
	"context"
	"testing"
	"time"
)

func TestMemStoreSnapshotRestorePressureLeaseReleaseAndExpiry(t *testing.T) {
	t.Parallel()
	store := NewMemStore()
	ctx := context.Background()

	first, err := store.AcquireSnapshotRestorePressure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	release, err := first.ReserveSnapshotRestore(ctx, "node-a", time.Minute)
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	first.Close()

	second, err := store.AcquireSnapshotRestorePressure(ctx)
	if err != nil {
		release()
		t.Fatal(err)
	}
	counts, err := second.ActiveSnapshotRestoreCounts(ctx)
	if err != nil {
		second.Close()
		release()
		t.Fatal(err)
	}
	if counts["node-a"] != 1 {
		t.Fatalf("active restore counts = %#v, want node-a:1", counts)
	}
	second.Close()

	release()
	third, err := store.AcquireSnapshotRestorePressure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts, err = third.ActiveSnapshotRestoreCounts(ctx)
	third.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Fatalf("counts after release = %#v, want empty", counts)
	}

	expiring, err := store.AcquireSnapshotRestorePressure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expiring.ReserveSnapshotRestore(ctx, "node-b", time.Millisecond); err != nil {
		expiring.Close()
		t.Fatal(err)
	}
	expiring.Close()
	time.Sleep(5 * time.Millisecond)
	last, err := store.AcquireSnapshotRestorePressure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts, err = last.ActiveSnapshotRestoreCounts(ctx)
	last.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Fatalf("counts after lease expiry = %#v, want empty", counts)
	}
}
