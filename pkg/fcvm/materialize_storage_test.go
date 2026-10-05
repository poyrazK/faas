package fcvm

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

type observedMaterializationBackend struct {
	storage.StorageBackend
	reads      atomic.Int64
	closes     atomic.Int64
	beforeLink func()
	linkErr    error
}

func (b *observedMaterializationBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	r, err := b.StorageBackend.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return &observedMaterializationReader{ReadCloser: r, backend: b}, nil
}

type observedMaterializationReader struct {
	io.ReadCloser
	backend *observedMaterializationBackend
}

func (r *observedMaterializationReader) Read(p []byte) (int, error) {
	r.backend.reads.Add(1)
	return r.ReadCloser.Read(p)
}

func (r *observedMaterializationReader) Close() error {
	r.backend.closes.Add(1)
	return r.ReadCloser.Close()
}

func (r *observedMaterializationReader) LinkTo(path string) error {
	if r.backend.beforeLink != nil {
		r.backend.beforeLink()
	}
	if r.backend.linkErr != nil {
		return r.backend.linkErr
	}
	return r.ReadCloser.(storage.LocalFileLinker).LinkTo(path)
}

// adr: 425
func TestMaterializeStorageRetainsCacheFileOrCopiesOpenedStream(t *testing.T) {
	for _, mode := range []string{"cache-miss", "oversized", "cross-device", "evicted"} {
		t.Run(mode, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			parent := &restoreResolutionBackend{}
			root := t.TempDir()
			budget := int64(1 << 20)
			if mode == "oversized" {
				budget = 4
			}
			cache, err := storage.NewLocalCacheBackend(parent, root, budget)
			if err != nil {
				t.Fatal(err)
			}
			backend := &observedMaterializationBackend{StorageBackend: cache}
			if mode == "cross-device" {
				backend.linkErr = syscall.EXDEV
			}
			if mode == "evicted" {
				backend.beforeLink = func() {
					if err := cache.Delete(context.Background(), "snap/dep/mem"); err != nil {
						t.Fatal(err)
					}
				}
			}
			v := NewJailerVMM(t.TempDir(), 0).WithStorage(backend)
			t.Cleanup(func() {
				if err := v.sweepMaterialised("instance"); err != nil {
					t.Error(err)
				}
			})
			path, timing, err := v.resolveRestoreBlob(context.Background(), "instance", "mem", "snap/dep/mem", "")
			if err != nil {
				t.Fatal(err)
			}
			if timing.Source != "materialized" || timing.Bytes != 6 {
				t.Fatalf("miss attribution changed: %#v", timing)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "remote" {
				t.Fatalf("materialized bytes: %q, %v", got, err)
			}
			wantLink := mode == "cache-miss" || mode == "oversized"
			if (backend.reads.Load() == 0) != wantLink {
				t.Fatalf("Read calls = %d, want link=%t", backend.reads.Load(), wantLink)
			}
			if backend.closes.Load() != 1 || parent.gets.Load() != 1 {
				t.Fatalf("closes=%d parent fetches=%d, want 1 each", backend.closes.Load(), parent.gets.Load())
			}
			if mode == "cache-miss" {
				src, err := os.Stat(storage.CacheFileForKey(root, "snap/dep/mem"))
				if err != nil {
					t.Fatal(err)
				}
				dst, err := os.Stat(path)
				if err != nil || !os.SameFile(src, dst) {
					t.Fatalf("materialization copied instead of retaining the cache inode: %v", err)
				}
				if err := cache.Delete(context.Background(), "snap/dep/mem"); err != nil {
					t.Fatal(err)
				}
			}
			// Writable staging must never write through the retained shared inode.
			jail := t.TempDir()
			name, err := stageWritableAs(jail, path, "drive.ext4", os.Getuid(), os.Getgid())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(jail, name), []byte("private"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err = os.ReadFile(path)
			if err != nil || string(got) != "remote" {
				t.Fatalf("writable staging changed shared source: %q, %v", got, err)
			}
			if err := v.sweepMaterialised("instance"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("teardown left materialization: %v", err)
			}
			left, err := os.ReadDir(tmp)
			if err != nil || len(left) != 0 {
				t.Fatalf("temporary materializations leaked: %v, %v", left, err)
			}
		})
	}
}
