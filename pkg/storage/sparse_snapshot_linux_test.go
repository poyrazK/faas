//go:build linux

package storage_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

func assertSparseSnapshot(t *testing.T, path string, data []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("snapshot bytes changed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	allocated := info.Sys().(*syscall.Stat_t).Blocks * 512
	if allocated >= int64(len(data))/4 {
		t.Fatalf("snapshot lost holes: allocated=%d logical=%d", allocated, len(data))
	}
}

func sparseCachePath(root, key string) string {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	return filepath.Join(root, hash[:2], hash[2:])
}

func TestSparseSnapshotLocalAndCachePublication(t *testing.T) {
	data := make([]byte, 8<<20+17)
	copy(data[2<<20:], bytes.Repeat([]byte{0x7a}, 4096))
	key := "snap/deployment/init/mem"
	t.Run("local", func(t *testing.T) {
		root := t.TempDir()
		backend, err := storage.NewLocalStorageBackend(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := backend.Put(t.Context(), key, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		assertSparseSnapshot(t, filepath.Join(root, key), data)
	})
	for _, mode := range []string{"put", "read-through"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			parent := newFakeBackend()
			cache, err := storage.NewLocalCacheBackend(parent, root, int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "put" {
				err = cache.Put(t.Context(), key, bytes.NewReader(data))
			} else {
				parent.blobs[key] = data
				var r io.ReadCloser
				r, err = cache.Get(t.Context(), key)
				if err == nil {
					_, err = io.Copy(io.Discard, r)
					_ = r.Close()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(parent.blobs[key], data) {
				t.Fatal("parent did not retain complete logical bytes")
			}
			assertSparseSnapshot(t, sparseCachePath(root, key), data)
			// Holes must not let the cache admit more logical snapshot bytes
			// than its existing budget. Publishing another entry evicts this one.
			if err := cache.Put(t.Context(), "apps/new/layer", bytes.NewReader([]byte{1})); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(sparseCachePath(root, key)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("logical cache budget was bypassed: %v", err)
			}
		})
	}
}
