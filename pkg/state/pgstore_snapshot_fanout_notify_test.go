package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgSnapshotFanoutNotifiesNewEvents pins the wake-up vmmd's snapshot
// replica worker waits on: a snapshot that gains a fan-out event sends
// snapshot_fanout, and rewriting its storage key, which adds no event, sends
// nothing.
func TestPgSnapshotFanoutNotifiesNewEvents(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, _, deploymentID := seedLiveDeploy(t, s, ctx, "snapshot-fanout-notify")
	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, "listen "+db.NotifySnapshotFanout); err != nil {
		t.Fatal(err)
	}
	notified := func(wait time.Duration) bool {
		waitCtx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		_, err := listener.Conn().WaitForNotification(waitCtx)
		return err == nil
	}

	snap, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: deploymentID, FCVersion: "fc-notify", MemBytes: 1024, DiskBytes: 2048,
		StorageKey: state.SnapMemKey(deploymentID),
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if !notified(5 * time.Second) {
		t.Fatal("a new fan-out event sent no snapshot_fanout")
	}
	if _, err := pool.Exec(ctx, `update snapshots set storage_key = storage_key where id = $1`, snap.ID); err != nil {
		t.Fatal(err)
	}
	if notified(300 * time.Millisecond) {
		t.Fatal("a snapshot rewrite that added no fan-out event sent snapshot_fanout")
	}
}
