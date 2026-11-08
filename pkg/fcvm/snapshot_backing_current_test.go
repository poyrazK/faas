package fcvm

import (
	"context"
	"testing"
)

// The verdict follows the checks a wake runs before it loads a snapshot
// (ADR-20261106214916).
func TestCheckSnapshotBackingCurrent(t *testing.T) {
	ctx := context.Background()
	check := func(t *testing.T, f backingFixture, snap *Snapshot, want SnapshotBackingVerdict) {
		t.Helper()
		got, err := f.m.CheckSnapshotBackingCurrent(ctx, snap, f.base)
		if got != want {
			t.Fatalf("verdict = %s (%v), want %s", got, err, want)
		}
		if (err == nil) != (want == SnapshotBackingCurrent) {
			t.Fatalf("verdict %s came with error %v", got, err)
		}
	}
	t.Run("same images are current", func(t *testing.T) {
		f := newBackingFixture(ctx, t)
		check(t, f, f.capture(ctx, t, "snap/d1/captures/c1/v2/mem"), SnapshotBackingCurrent)
	})
	t.Run("a replaced base is stale", func(t *testing.T) {
		f := newBackingFixture(ctx, t)
		snap := f.capture(ctx, t, "snap/d1/captures/c1/v2/mem")
		replaceFile(t, f.base, "base-layout-b")
		check(t, f, snap, SnapshotBackingStale)
	})
	t.Run("a replaced kernel is stale", func(t *testing.T) {
		f := newBackingFixture(ctx, t)
		snap := f.capture(ctx, t, "snap/d1/captures/c1/v2/mem")
		replaceFile(t, f.kernel, "kernel-v2")
		check(t, f, snap, SnapshotBackingStale)
	})
	t.Run("another Firecracker version is stale", func(t *testing.T) {
		f := newBackingFixture(ctx, t)
		snap := f.capture(ctx, t, "snap/d1/captures/c1/v2/mem")
		snap.FCVersion = "0.0.1"
		check(t, f, snap, SnapshotBackingStale)
	})
	t.Run("a capture without identity is stale", func(t *testing.T) {
		f := newBackingFixture(ctx, t)
		check(t, f, &Snapshot{FCVersion: testFCVersion, StorageKey: "snap/d1/captures/legacy/v2/mem"}, SnapshotBackingStale)
	})
	t.Run("this node's base cannot be read", func(t *testing.T) {
		f := newBackingFixture(ctx, t)
		snap := f.capture(ctx, t, "snap/d1/captures/c1/v2/mem")
		got, err := f.m.CheckSnapshotBackingCurrent(ctx, snap, f.base+".missing")
		if got != SnapshotBackingUnknown || err == nil {
			t.Fatalf("verdict = %s (%v), want unknown with an error", got, err)
		}
	})
	t.Run("missing inputs are unknown", func(t *testing.T) {
		f := newBackingFixture(ctx, t)
		if got, err := f.m.CheckSnapshotBackingCurrent(ctx, &Snapshot{}, f.base); got != SnapshotBackingUnknown || err == nil {
			t.Fatalf("verdict = %s (%v), want unknown with an error", got, err)
		}
	})
}
