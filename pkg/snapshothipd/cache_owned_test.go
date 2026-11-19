package snapshothipd

// adr: 20261118160000

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

const (
	ownedCapture = "snap/dep-owned/captures/c-owned/v2/"
	ownedLayer   = "apps/web/dep-owned.ext4"
)

type fakeOwnedRetention struct {
	fakeRetention
	owned    state.NodeOwnedSnapshots
	requeued []string
}

func (f *fakeOwnedRetention) NodeOwnedSnapshots(context.Context, string) (state.NodeOwnedSnapshots, error) {
	return f.owned, nil
}

func (f *fakeOwnedRetention) RequeueOwnedSnapshotReplicas(_ context.Context, nodeID string, _ time.Duration) (int, error) {
	f.requeued = append(f.requeued, nodeID)
	return 1, nil
}

// TestOwnedLiveSnapshotsAreEvictedLast: the live capture and layer of an app
// this node owns outlast every other live snapshot; another node's live
// snapshot keeps the shared live tier.
func TestOwnedLiveSnapshotsAreEvictedLast(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	ret := testRetention(now)
	ret.Captures[ownedCapture] = state.CaptureLive
	ret.LiveLayers[ownedLayer] = true
	d := cacheRetentionDecision{ret: ret, now: now, owned: state.NodeOwnedSnapshots{
		Captures: map[string]bool{ownedCapture: true},
		Layers:   map[string]bool{ownedLayer: true},
	}}
	for key, want := range map[string]int{
		ownedCapture + "mem":   storage.EvictOwned,
		ownedCapture + "drive": storage.EvictOwned,
		ownedLayer:             storage.EvictOwned,
		liveCapture + "mem":    storage.EvictLast,
		liveLayer:              storage.EvictLast,
		staleCapture + "mem":   storage.EvictFirst,
	} {
		if got := d.tier(key); got != want {
			t.Errorf("tier(%s) = %d, want %d", key, got, want)
		}
	}
	if storage.EvictOwned <= storage.EvictLast {
		t.Fatal("owned snapshots must be evicted after other live snapshots")
	}
}

// TestSweepRequeuesOwnedSnapshots: each sweep asks the store to queue the
// owned snapshots this node does not hold, so an app that just moved here, or
// whose snapshot the cache evicted, is fetched before its next wake.
func TestSweepRequeuesOwnedSnapshots(t *testing.T) {
	reader := &fakeOwnedRetention{fakeRetention: fakeRetention{ret: testRetention(time.Now())}}
	r := New(nil, &fakeBackend{objects: map[string][]byte{}}, "node-owner", slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithCacheRetention(reader)
	r.sweepCache(context.Background(), time.Now())
	if len(reader.requeued) != 1 || reader.requeued[0] != "node-owner" {
		t.Fatalf("requeued = %v, want one requeue for node-owner", reader.requeued)
	}
}
