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
