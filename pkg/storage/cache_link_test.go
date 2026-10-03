package storage_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

// adr: 425
func TestCacheReaderLinkRetainsOpenedArtifact(t *testing.T) {
	for _, mode := range []string{"miss", "hit", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			parent := newFakeBackend()
			const key = "snap/dep/mem"
			parent.blobs[key] = []byte("original snapshot")
			budget := int64(1 << 20)
			if mode == "oversized" {
				budget = 4
			}
			cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), budget)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "hit" {
				r, err := cache.Get(ctx, key)
				if err != nil {
					t.Fatal(err)
				}
				_ = r.Close()
			}
			r, err := cache.Get(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			linker, ok := r.(storage.LocalFileLinker)
			if !ok {
				t.Fatal("cache reader has no LocalFileLinker capability")
			}
			// Linking retains the whole artifact even after a partial stream read.
			prefix := make([]byte, 3)
			if _, err := io.ReadFull(r, prefix); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(t.TempDir(), "retained")
			if err := linker.LinkTo(link); err != nil {
				t.Fatal(err)
			}
			remainder, err := io.ReadAll(r)
			if err != nil || string(remainder) != "ginal snapshot" {
				t.Fatalf("link changed reader position: %q, %v", remainder, err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			if err := cache.Delete(ctx, key); err != nil {
				t.Fatal(err)
			}
			assertLinkContents(t, link, "original snapshot")
			if parent.gets.Load() != 1 {
				t.Fatalf("parent fetches = %d, want 1", parent.gets.Load())
			}
		})
	}
}

// adr: 425
func TestCacheReaderLinkRejectsReplacementAndPreservesStream(t *testing.T) {
	ctx := context.Background()
	parent := newFakeBackend()
	const key = "snap/dep/mem"
	parent.blobs[key] = []byte("original")
	cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	old, err := cache.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	retained := filepath.Join(t.TempDir(), "before-refresh")
	if err := old.(storage.LocalFileLinker).LinkTo(retained); err != nil {
		t.Fatal(err)
	}
	parent.blobs[key] = []byte("replacement")
	fresh, err := cache.Refresh(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	_ = fresh.Close()
	assertLinkContents(t, retained, "original")
	wrongLink := filepath.Join(t.TempDir(), "after-refresh")
	if err := old.(storage.LocalFileLinker).LinkTo(wrongLink); err == nil {
		t.Fatal("linked a replacement instead of the opened snapshot")
	}
	if _, err := os.Lstat(wrongLink); !os.IsNotExist(err) {
		t.Fatalf("failed link left a file: %v", err)
	}
	got, err := io.ReadAll(old)
	if err != nil || string(got) != "original" {
		t.Fatalf("copy fallback no longer reads original: %q, %v", got, err)
	}
}

// adr: 425
func TestCacheReaderLinkDoesNotReplaceDestination(t *testing.T) {
	parent := newFakeBackend()
	parent.blobs["snap/dep/mem"] = []byte("snapshot")
	cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	r, err := cache.Get(context.Background(), "snap/dep/mem")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	dst := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(dst, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.(storage.LocalFileLinker).LinkTo(dst); err == nil {
		t.Fatal("LinkTo replaced an existing destination")
	}
	assertLinkContents(t, dst, "keep me")
}

// adr: 425
func TestCacheReaderLinkSurvivesBudgetEviction(t *testing.T) {
	ctx := context.Background()
	parent := newFakeBackend()
	const key = "snap/old/mem"
	parent.blobs[key] = []byte(strings.Repeat("a", 4096))
	root := t.TempDir()
	cache, err := storage.NewLocalCacheBackend(parent, root, 8192)
	if err != nil {
		t.Fatal(err)
	}
	r, err := cache.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "retained")
	if err := r.(storage.LocalFileLinker).LinkTo(link); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if err := cache.Put(ctx, "snap/new/mem", strings.NewReader(strings.Repeat("b", 8192))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(storage.CacheFileForKey(root, key)); !os.IsNotExist(err) {
		t.Fatalf("old entry was not evicted: %v", err)
	}
	assertLinkContents(t, link, strings.Repeat("a", 4096))
}

func assertLinkContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("retained contents = %q, %v; want %q", got, err, want)
	}
}
