// adr: 1135
package storage

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// TestCacheMissMaterializeBench times a node-cache miss of a padded app drive
// in both remote encodings. Production stores app drives as zstd in GCS (a
// 2 GiB drive with ~90 MiB of files is a ~90 MB object), yet compute-1 logged
// 4–11 s per miss: the plain encoding makes the fill decode and scan every
// logical byte. The extent encoding (ADR-1135) carries only the data extents.
// Fills alternate between the encodings so host load hits both alike; the
// object comes from an in-memory store, so no network time is included.
//
// Opt-in: FAAS_STORAGE_MISS_BENCH=1. FAAS_STORAGE_MISS_BENCH_LOGICAL_MIB (2048)
// and FAAS_STORAGE_MISS_BENCH_DATA_MIB (120) shape the drive;
// FAAS_STORAGE_MISS_BENCH_DIR places the files (production's cache is XFS).
func TestCacheMissMaterializeBench(t *testing.T) {
	if os.Getenv("FAAS_STORAGE_MISS_BENCH") != "1" {
		t.Skip("set FAAS_STORAGE_MISS_BENCH=1 to run the cache-miss materialize harness")
	}
	logicalMiB := missBenchMiB(t, "FAAS_STORAGE_MISS_BENCH_LOGICAL_MIB", 2048)
	dataMiB := missBenchMiB(t, "FAAS_STORAGE_MISS_BENCH_DATA_MIB", 120)
	dir := os.Getenv("FAAS_STORAGE_MISS_BENCH_DIR")
	if dir == "" {
		dir = t.TempDir()
	} else {
		var err error
		if dir, err = os.MkdirTemp(dir, "miss-bench-*"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	ctx := context.Background()

	// An ext4 image: data in 1 MiB extents spread across the logical size,
	// zeros everywhere else.
	src := filepath.Join(dir, "src.ext4")
	writeSparseDrive(t, src, logicalMiB, dataMiB)
	t.Logf("host %d CPUs (GOMAXPROCS %d); drive %d MiB logical, %d MiB data",
		runtime.NumCPU(), runtime.GOMAXPROCS(0), logicalMiB, dataMiB)

	const key = "apps/bench/drive.ext4"
	backends := map[string]*GCSStorageBackend{}
	for _, name := range []string{"zstd", "zstd-extents"} {
		store := newMemoryGCSStore()
		backend, err := newGCSStorageBackend("bench-bucket", snapshotCompressionZstd, store)
		if err != nil {
			t.Fatal(err)
		}
		backend.WithSparseExtents(name == "zstd-extents")
		f, err := os.Open(src) //nolint:forbidigo // bench fixture under its own temp dir.
		if err != nil {
			t.Fatal(err)
		}
		put := timeIt(t, func() error { return backend.Put(ctx, key, f) })
		_ = f.Close()
		object := store.objects["bench-bucket/"+key]
		t.Logf("%-13s object %.1f MB, encoding %q, compress+put %s",
			name, float64(len(object.body))/1e6, object.metadata[gcsEncodingMetadata], put.Round(time.Millisecond))
		backends[name] = backend
	}

	fill := func(backend *GCSStorageBackend) time.Duration {
		return timeIt(t, func() error {
			cacheDir, err := os.MkdirTemp(dir, "cache-*")
			if err != nil {
				return err
			}
			defer func() { _ = os.RemoveAll(cacheDir) }()
			cache, err := NewLocalCacheBackend(backend, cacheDir, 64<<30)
			if err != nil {
				return err
			}
			rc, err := cache.Get(ctx, key)
			if err != nil {
				return err
			}
			return rc.Close()
		})
	}
	for rep := 0; rep < 3; rep++ {
		plain := fill(backends["zstd"])
		extents := fill(backends["zstd-extents"])
		t.Logf("rep %d: cache fill zstd %s | zstd-extents %s", rep, plain.Round(time.Millisecond), extents.Round(time.Millisecond))
	}
}

func timeIt(t *testing.T, fn func() error) time.Duration {
	t.Helper()
	started := time.Now()
	if err := fn(); err != nil {
		t.Fatal(err)
	}
	return time.Since(started)
}

func writeSparseDrive(t *testing.T, path string, logicalMiB, dataMiB int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Truncate(int64(logicalMiB) << 20); err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	stride := int64(logicalMiB/dataMiB) << 20
	for i := 0; i < dataMiB; i++ {
		if _, err := rand.Read(chunk); err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteAt(chunk, int64(i)*stride); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
}

func missBenchMiB(t *testing.T, name string, def int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 16384 {
		t.Fatalf("%s=%q must be an integer MiB in [1,16384]", name, raw)
	}
	return n
}
