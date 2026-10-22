// adr: 20261022184531
package storage_test

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

// A readiness probe reads what shared storage holds without changing what
// the node serves: ReadParent neither reads nor replaces the cached copy.
func TestLocalCacheBackend_ReadParentLeavesTheCacheAlone(t *testing.T) {
	ctx := context.Background()
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Put(ctx, "scans/base.scan.json", strings.NewReader("served")); err != nil {
		t.Fatal(err)
	}
	if got, err := readAll(ctx, cache, "scans/base.scan.json"); err != nil || got != "served" {
		t.Fatalf("prime Get = %q, %v", got, err)
	}
	parent.blobs["scans/base.scan.json"] = []byte("published")
	rc, err := cache.ReadParent(ctx, "scans/base.scan.json")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(b) != "published" {
		t.Fatalf("ReadParent = %q, want the canonical copy", b)
	}
	if got, err := readAll(ctx, cache, "scans/base.scan.json"); err != nil || got != "served" {
		t.Fatalf("cached copy after ReadParent = %q, %v; want it unchanged", got, err)
	}
	if _, cached, err := cache.LocalPath("scans/other.json"); err != nil || cached {
		t.Fatalf("LocalPath of an unread key = %t, %v", cached, err)
	}
	if _, err := cache.ReadParent(ctx, "scans/missing.json"); err == nil {
		t.Fatal("ReadParent of a missing key succeeded")
	}
}
