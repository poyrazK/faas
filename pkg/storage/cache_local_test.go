package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"
)

type remoteOnlyBackend struct{ objects map[string][]byte }

func (r *remoteOnlyBackend) Put(_ context.Context, key string, rd io.Reader) error {
	b, err := io.ReadAll(rd)
	if err != nil {
		return err
	}
	r.objects[key] = b
	return nil
}

func (r *remoteOnlyBackend) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := r.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (r *remoteOnlyBackend) Delete(_ context.Context, key string) error {
	delete(r.objects, key)
	return nil
}

func TestCachedLocallyDoesNotTouchTheEntry(t *testing.T) {
	parent := &remoteOnlyBackend{objects: map[string][]byte{}}
	cache, err := NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := cache.Put(ctx, "snap/d/mem", bytes.NewReader([]byte("memory"))); err != nil {
		t.Fatal(err)
	}
	path, _ := cache.cacheFileFor("snap/d/mem")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	cached, known, err := CachedLocally(cache, "snap/d/mem")
	if err != nil || !known || !cached {
		t.Fatalf("CachedLocally(cached) = %v %v %v", cached, known, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Fatalf("presence check touched the LRU mtime: %s, want %s", fi.ModTime(), old)
	}
	if cached, known, err := CachedLocally(cache, "snap/d/drive"); err != nil || !known || cached {
		t.Fatalf("CachedLocally(missing) = %v %v %v, want false true nil", cached, known, err)
	}
	if _, known, err := CachedLocally(parent, "snap/d/mem"); err != nil || known {
		t.Fatalf("CachedLocally(remote-only) known=%v err=%v, want unknown", known, err)
	}
}

// LocalPathWithoutTouch serves vmmd's periodic ADR-510 digest primer: it must
// name the same file LocalPath does without queueing an LRU touch, or the
// primer would keep every base it follows resident.
func TestLocalPathWithoutTouch(t *testing.T) {
	ctx := context.Background()
	parent := &remoteOnlyBackend{objects: map[string][]byte{}}
	cache, err := NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(ctx, "base/releases/b.ext4", bytes.NewReader([]byte("base"))); err != nil {
		t.Fatal(err)
	}
	local, err := NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Put(ctx, "base/b.ext4", bytes.NewReader([]byte("base"))); err != nil {
		t.Fatal(err)
	}
	cachedPath, _ := cache.cacheFileFor("base/releases/b.ext4")
	localPath, _, _ := local.LocalPath("base/b.ext4")
	for _, tc := range []struct {
		name      string
		root      StorageBackend
		key       string
		wantPath  string
		wantLocal bool
	}{
		{name: "cached entry", root: cache, key: "base/releases/b.ext4", wantPath: cachedPath, wantLocal: true},
		{name: "missing entry", root: cache, key: "base/releases/other.ext4"},
		{name: "local backend", root: local, key: "base/b.ext4", wantPath: localPath, wantLocal: true},
		{name: "remote-only backend", root: parent, key: "base/releases/b.ext4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, ok, err := LocalPathWithoutTouch(tc.root, tc.key)
			if err != nil || ok != tc.wantLocal || path != tc.wantPath {
				t.Fatalf("LocalPathWithoutTouch = %q %v %v, want %q %v nil", path, ok, err, tc.wantPath, tc.wantLocal)
			}
			if _, pending := cache.touchPending.Load(cachedPath); pending {
				t.Fatal("LocalPathWithoutTouch queued an LRU touch")
			}
		})
	}
}
