// adr: 20261122132055 §2 — a node-first write stays in this node's cache
// only, pinned against eviction, until PublishLocal gives the parent a copy;
// a restarted process finds what is left to upload, and an upload of a key
// deleted meanwhile leaves the parent without it.
package storage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

func TestLocalOnlyEntriesStayUntilPublished(t *testing.T) {
	parent := newFakeBackend()
	// An eight-byte budget is less than one block, so eviction takes every
	// unpinned entry it can.
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 8)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const mem = "snap/sandbox-session-acct/captures/c1/v2/mem"
	if err := storage.NodeFirst(cache).Put(ctx, mem, strings.NewReader("memory")); err != nil {
		t.Fatalf("node-first put: %v", err)
	}
	if parent.puts.Load() != 0 || !cache.LocalOnly(mem) {
		t.Fatalf("a node-first put reached the parent (%d puts) or is not local-only", parent.puts.Load())
	}
	// Another write over budget must not evict the only copy.
	if err := cache.Put(ctx, "snap/other/captures/x/v2/mem", strings.NewReader("other")); err != nil {
		t.Fatal(err)
	}
	if got, err := readAll(ctx, cache, mem); err != nil || got != "memory" {
		t.Fatalf("local-only entry after eviction = %q, %v", got, err)
	}
	if keys, err := cache.LocalOnlyKeys("snap/sandbox-session-"); err != nil || len(keys) != 1 || keys[0] != mem {
		t.Fatalf("local-only keys = %v, %v", keys, err)
	}
	// A restarted process finishes the uploads it finds.
	if n, err := storage.PublishLocalPrefix(ctx, cache, "snap/sandbox-session-"); err != nil || n != 1 {
		t.Fatalf("publish the prefix = %d, %v", n, err)
	}
	if cache.LocalOnly(mem) {
		t.Fatal("still local-only after its upload")
	}
	if got, err := readAll(ctx, parent, mem); err != nil || got != "memory" {
		t.Fatalf("parent copy = %q, %v", got, err)
	}
	if published, err := cache.PublishLocal(ctx, mem); err != nil || published {
		t.Fatalf("a second publish = %v, %v; want nothing to do", published, err)
	}
	if keys, _ := cache.LocalOnlyKeys(""); len(keys) != 0 {
		t.Fatalf("local-only keys after the upload = %v", keys)
	}
}

func TestLocalOnlyDeleteDropsTheMark(t *testing.T) {
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const key = "snap/sandbox-session-acct/captures/c2/v2/vmstate"
	if err := cache.PutLocal(ctx, key, strings.NewReader("state")); err != nil {
		t.Fatal(err)
	}
	if err := cache.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("delete: %v", err)
	}
	if cache.LocalOnly(key) {
		t.Fatal("a deleted key is still local-only")
	}
	if published, err := cache.PublishLocal(ctx, key); err != nil || published || parent.puts.Load() != 0 {
		t.Fatalf("publish of a deleted key = %v, %v (%d parent puts)", published, err, parent.puts.Load())
	}
}

// A local parent names a path for every key. Until the upload, a local-only
// entry's path is the cache's copy; after it, the parent's.
func TestLocalOnlyEntryLocalPathBeforeAndAfterUpload(t *testing.T) {
	ctx := context.Background()
	parent, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatal(err)
	}
	const drive = "snap/sandbox-session-acct/captures/c1/v2/drive"
	if err := storage.NodeFirst(cache).Put(ctx, drive, strings.NewReader("drive")); err != nil {
		t.Fatal(err)
	}
	path, source, ok, err := cache.LocalPathWithSource(drive)
	if err != nil || !ok || source != storage.LocalPathSourceCache || !strings.HasPrefix(path, cache.Root()) {
		t.Fatalf("before the upload: %q %q %v %v; want the cache's copy", path, source, ok, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "drive" {
		t.Fatalf("before the upload reads %q, %v", data, err)
	}
	if _, err := cache.PublishLocal(ctx, drive); err != nil {
		t.Fatal(err)
	}
	path, source, ok, err = cache.LocalPathWithSource(drive)
	if err != nil || !ok || source != storage.LocalPathSourceBackend || strings.HasPrefix(path, cache.Root()) {
		t.Fatalf("after the upload: %q %q %v %v; want the parent's copy", path, source, ok, err)
	}
}
