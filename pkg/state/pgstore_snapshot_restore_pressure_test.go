//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreSnapshotRestorePressureLeaseReleaseAndExpiry(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	nodeID := resolveDefaultLocal(t, ctx, store)

	first, err := store.AcquireSnapshotRestorePressure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	release, err := first.ReserveSnapshotRestore(ctx, nodeID, time.Minute)
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	first.Close()

	secondStore := state.NewPgStore(pool)
	second, err := secondStore.AcquireSnapshotRestorePressure(ctx)
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
	if counts[nodeID] != 1 {
		t.Fatalf("active restore counts = %#v, want default-local:1", counts)
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
	if _, err := expiring.ReserveSnapshotRestore(ctx, nodeID, time.Millisecond); err != nil {
		expiring.Close()
		t.Fatal(err)
	}
	expiring.Close()
	time.Sleep(10 * time.Millisecond)
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
