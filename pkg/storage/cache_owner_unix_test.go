//go:build unix

package storage_test

// adr: 20261114182830

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
)

// TestNonOwnerLeavesCapturesAndLayersToTheOwner: two processes share one
// cache directory. While one owns eviction, the other may still free shared
// bases but never a snapshot capture or an app layer; once the owner is
// gone, it evicts as before.
func TestNonOwnerLeavesCapturesAndLayersToTheOwner(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "cache")
	const (
		capture = "snap/d/captures/c1/v2/mem"
		layer   = "apps/web/dep.ext4"
		base    = "base/releases/runner-node22-amd64.ext4"
	)
	keys := []string{capture, layer, base} // oldest first
	parent := newFakeBackend()
	owner, err := storage.NewLocalCacheBackend(parent, dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := storage.NewLocalCacheBackend(parent, dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	epoch := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, k := range keys {
		parent.blobs[k] = []byte("blob " + k)
		_, rc := getString(t, ctx, other, k)
		_ = rc.Close()
		when := epoch.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(storage.CacheFileForKey(dir, k), when, when); err != nil {
			t.Fatal(err)
		}
	}
	const unit = int64(1) << 30
	other.SetDiskSpaceForTest(func(string) (int64, int64, error) {
		var gone int64
		for _, k := range keys {
			if _, err := os.Stat(storage.CacheFileForKey(dir, k)); err != nil {
				gone++
			}
		}
		return gone * unit, 100 * unit, nil
	})

	release, err := owner.ClaimEvictionOwnership()
	if err != nil {
		t.Fatal(err)
	}
	// Asks for all three units; only the base may go.
	if err := other.ReclaimFreeBytes(3 * unit); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]bool{capture: true, layer: true, base: false} {
		if got := cached(t, other, k); got != want {
			t.Errorf("owner held: %s cached = %v, want %v", k, got, want)
		}
	}

	release()
	if err := other.ReclaimFreeBytes(3 * unit); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{capture, layer} {
		if cached(t, other, k) {
			t.Errorf("owner gone: %s still cached; a cache without vmmd must evict as before", k)
		}
	}
}

// TestEvictionOwnershipIsExclusive: a second claim on one directory fails
// while the first is held, and succeeds after it is released.
func TestEvictionOwnershipIsExclusive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	a, err := storage.NewLocalCacheBackend(newFakeBackend(), dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := storage.NewLocalCacheBackend(newFakeBackend(), dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	release, err := a.ClaimEvictionOwnership()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.ClaimEvictionOwnership(); err == nil {
		t.Fatal("second claim succeeded while the first was held")
	}
	release()
	releaseB, err := b.ClaimEvictionOwnership()
	if err != nil {
		t.Fatalf("claim after release: %v", err)
	}
	releaseB()
}
