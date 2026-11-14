package snapshothipd

// adr: 20261114120000

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type fakeRetention struct {
	ret state.NodeCacheRetention
	err error
}

func (f fakeRetention) NodeCacheRetention(context.Context) (state.NodeCacheRetention, error) {
	return f.ret, f.err
}

const (
	liveCapture    = "snap/dep-a/captures/c-live/v2/"
	staleCapture   = "snap/dep-b/captures/c-stale/v2/"
	branchCapture  = "snap/dep-c/captures/c-branch/v2/"
	orphanCapture  = "snap/dep-d/captures/c-orphan/v2/"
	freshCapture   = "snap/dep-e/captures/c-fresh/v2/"
	keptRelease    = "base/releases/runner-node22-amd64-kept.ext4"
	unusedRelease  = "base/releases/runner-node22-amd64-unused.ext4"
	newRelease     = "base/releases/runner-node22-amd64-new.ext4"
	unknownRelease = "base/releases/runner-node22-amd64-unknown.ext4"
	legacyMem      = "snap/dep-f/mem"
	layer          = "layers/dep-a.ext4"
)

func testRetention(now time.Time) state.NodeCacheRetention {
	old := now.Add(-2 * api.RuntimeReleaseCacheGrace)
	return state.NodeCacheRetention{
		Captures: map[string]state.CaptureUse{
			liveCapture:   state.CaptureLive,
			staleCapture:  state.CaptureStale,
			branchCapture: state.CaptureOther,
		},
		RuntimeReleases: map[string]state.RuntimeReleaseRetention{
			keptRelease:   {CreatedAt: old, Keep: true},
			unusedRelease: {CreatedAt: old},
			newRelease:    {CreatedAt: now.Add(-time.Hour)},
		},
	}
}

// TestCacheRetentionDecisions: a live snapshot is evicted last and never
// swept; a stale one is evicted first but kept until imaged deletes its row;
// a capture no row references is swept once it is past the orphan grace (a
// younger one may still be waiting for its row); a runtime release goes only
// when nothing uses it and its grace has passed; legacy keys, layers and
// bases are never swept.
func TestCacheRetentionDecisions(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	d := cacheRetentionDecision{ret: testRetention(now), now: now}
	old := now.Add(-2 * api.SnapshotCacheOrphanGrace)
	for _, tc := range []struct {
		key       string
		modTime   time.Time
		tier      int
		removable bool
	}{
		{liveCapture + "drive", old, storage.EvictLast, false},
		{staleCapture + "mem", old, storage.EvictFirst, false},
		{branchCapture + "vmstate", old, storage.EvictNormal, false},
		{orphanCapture + "mem", old, storage.EvictNormal, true},
		{freshCapture + "mem", now.Add(-time.Hour), storage.EvictNormal, false},
		{keptRelease, old, storage.EvictNormal, false},
		{unusedRelease, now, storage.EvictFirst, true},
		{newRelease, old, storage.EvictNormal, false},
		{unknownRelease, now.Add(-2 * api.RuntimeReleaseCacheGrace), storage.EvictNormal, true},
		{unknownRelease, now.Add(-time.Hour), storage.EvictNormal, false},
		{legacyMem, old, storage.EvictNormal, false},
		{layer, old, storage.EvictNormal, false},
	} {
		if got := d.tier(tc.key); got != tc.tier {
			t.Errorf("tier(%s) = %d, want %d", tc.key, got, tc.tier)
		}
		e := storage.CachedEntry{Key: tc.key, ModTime: tc.modTime}
		if got := d.removable(e); got != tc.removable {
			t.Errorf("removable(%s, %s old) = %v, want %v", tc.key, now.Sub(tc.modTime), got, tc.removable)
		}
	}
}

// TestSweepCacheRemovesOnlyUnreferenced runs a sweep over a real cache: the
// orphaned capture and the unused release go, everything else stays, and the
// parent keeps every object.
func TestSweepCacheRemovesOnlyUnreferenced(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	keys := []string{liveCapture + "mem", orphanCapture + "mem", orphanCapture + "drive", unusedRelease, keptRelease, layer}
	parent := &fakeBackend{objects: map[string][]byte{}}
	for _, k := range keys {
		parent.objects[k] = []byte("bytes of " + k)
	}
	cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	old := now.Add(-2 * api.SnapshotCacheOrphanGrace)
	for _, k := range keys {
		rc, err := cache.Get(ctx, k)
		if err != nil {
			t.Fatalf("Get %s: %v", k, err)
		}
		_, _ = io.Copy(io.Discard, rc)
		_ = rc.Close()
		if err := os.Chtimes(storage.CacheFileForKey(cache.Root(), k), old, old); err != nil {
			t.Fatal(err)
		}
	}
	r := &Runner{backend: cache, nodeID: "node-1", log: slog.Default(), retention: fakeRetention{ret: testRetention(now)}}
	r.sweepCache(ctx, now)
	gone := map[string]bool{orphanCapture + "mem": true, orphanCapture + "drive": true, unusedRelease: true}
	for _, k := range keys {
		_, local, err := cache.LocalPath(k)
		if err != nil {
			t.Fatalf("LocalPath %s: %v", k, err)
		}
		if local == gone[k] {
			t.Errorf("%s cached = %v after the sweep, want %v", k, local, !gone[k])
		}
		if _, ok := parent.objects[k]; !ok {
			t.Errorf("the sweep removed %s from the parent", k)
		}
	}
}

// TestSweepCacheKeepsEverythingWhenReferencesFail: without the references the
// sweep cannot tell an orphan from a live capture, so it removes nothing.
func TestSweepCacheKeepsEverythingWhenReferencesFail(t *testing.T) {
	ctx := context.Background()
	key := orphanCapture + "mem"
	parent := &fakeBackend{objects: map[string][]byte{key: []byte("memory")}}
	cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := cache.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	old := time.Now().Add(-2 * api.SnapshotCacheOrphanGrace)
	if err := os.Chtimes(storage.CacheFileForKey(cache.Root(), key), old, old); err != nil {
		t.Fatal(err)
	}
	r := &Runner{backend: cache, nodeID: "node-1", log: slog.Default(), retention: fakeRetention{err: errors.New("db down")}}
	r.sweepCache(ctx, time.Now())
	if _, local, _ := cache.LocalPath(key); !local {
		t.Fatal("a failed reference read removed a cached capture")
	}
}
