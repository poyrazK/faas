package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type cacheTouchParent struct{}

func (cacheTouchParent) Put(context.Context, string, io.Reader) error { return nil }
func (cacheTouchParent) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, ErrNotFound
}
func (cacheTouchParent) Delete(context.Context, string) error { return nil }

func TestLocalPathDoesNotWaitForCacheTouch(t *testing.T) {
	cache, err := NewLocalCacheBackend(cacheTouchParent{}, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	const key = "snap/deployment/mem"
	path, _ := cache.cacheFileFor(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("snapshot"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int64
	cache.chtimes = func(string, time.Time, time.Time) error {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return nil
	}

	result := make(chan error, 1)
	go func() {
		got, source, ok, pathErr := cache.LocalPathWithSource(key)
		if pathErr == nil && (!ok || got != path) {
			pathErr = ErrNotFound
		}
		if pathErr == nil && source != LocalPathSourceCache {
			pathErr = fmt.Errorf("source = %q, want %q", source, LocalPathSourceCache)
		}
		result <- pathErr
	}()
	select {
	case pathErr := <-result:
		if pathErr != nil {
			t.Fatalf("LocalPath: %v", pathErr)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("LocalPath waited for the cache timestamp write")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("cache timestamp worker did not start")
	}

	// Repeated reads while the write is blocked must coalesce and remain fast.
	for i := 0; i < 32; i++ {
		if _, ok, pathErr := cache.LocalPath(key); pathErr != nil || !ok {
			t.Fatalf("LocalPath repeat %d: ok=%t err=%v", i, ok, pathErr)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("timestamp writes while first is pending = %d, want 1", got)
	}
	close(release)
}

func TestLocalCacheBackendCloseStopsTouchWorker(t *testing.T) {
	cache, err := NewLocalCacheBackend(cacheTouchParent{}, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-cache.touchDone:
	default:
		t.Fatal("cache touch worker is still running after Close")
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
