package fcvm

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

// Hide only LinkTo, retaining the *os.File WriteTo optimization used by the
// old copy path (including Linux copy_file_range). This comparison excludes
// network/cache-fill time and measures the redundant VMMD materialization.
type copyMaterializationBackend struct{ storage.StorageBackend }

func (b copyMaterializationBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	r, err := b.StorageBackend.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return struct {
		io.ReadCloser
		io.WriterTo
	}{r, r.(io.WriterTo)}, nil
}

// adr: 425
func BenchmarkMaterializeOpenedCacheFile(b *testing.B) {
	root := b.TempDir()
	b.Setenv("TMPDIR", root)
	parentRoot := filepath.Join(root, "parent")
	if err := os.MkdirAll(filepath.Join(parentRoot, "snap/dep"), 0o700); err != nil {
		b.Fatal(err)
	}
	const key = "snap/dep/mem"
	f, err := os.Create(filepath.Join(parentRoot, key))
	if err != nil {
		b.Fatal(err)
	}
	if err := f.Truncate(256 << 20); err != nil {
		b.Fatal(err)
	}
	page := make([]byte, 4096)
	for i := range page {
		page[i] = 1
	}
	for offset := int64(0); offset < 256<<20; offset += 1 << 20 {
		if _, err := f.WriteAt(page, offset); err != nil {
			b.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}
	parent, err := storage.NewLocalStorageBackend(parentRoot)
	if err != nil {
		b.Fatal(err)
	}
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(root, "cache"), 512<<20)
	if err != nil {
		b.Fatal(err)
	}
	r, err := cache.Get(context.Background(), key)
	if err != nil {
		b.Fatal(err)
	}
	_ = r.Close()
	for _, mode := range []string{"copy", "retain"} {
		b.Run(mode, func(b *testing.B) {
			var backend storage.StorageBackend = cache
			if mode == "copy" {
				backend = copyMaterializationBackend{cache}
			}
			v := NewJailerVMM(root, 0).WithStorage(backend)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := v.materializeFromStorage(context.Background(), "bench", key); err != nil {
					b.Fatal(err)
				}
				if err := v.sweepMaterialised("bench"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
