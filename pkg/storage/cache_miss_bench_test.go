package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

// TestCacheMissMaterializeBench times a node-cache miss of a padded app drive.
// Production stores app drives as zstd in GCS (a 2 GiB drive with ~90 MiB of
// files is a ~90 MB object), yet compute-1 logged 4–11 s per miss. This
// harness separates the local work by layer: decode alone, decode plus the
// sparse cache write, and the full LocalCacheBackend fill. The object comes
// from an in-memory store, so no network time is included.
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

	store := newMemoryGCSStore()
	backend, err := newGCSStorageBackend("bench-bucket", snapshotCompressionZstd, store)
	if err != nil {
		t.Fatal(err)
	}
	const key = "apps/bench/drive.ext4"
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := backend.Put(ctx, key, f); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	compressed := len(store.objects["bench-bucket/"+key].body)
	t.Logf("host %d CPUs (GOMAXPROCS %d); drive %d MiB logical, %d MiB data; object %.1f MB; compress+put %s",
		runtime.NumCPU(), runtime.GOMAXPROCS(0), logicalMiB, dataMiB, float64(compressed)/1e6, time.Since(started).Round(time.Millisecond))

	for rep := 0; rep < 3; rep++ {
		decode := timeIt(t, func() error {
			rc, err := backend.Get(ctx, key)
			if err != nil {
				return err
			}
			defer func() { _ = rc.Close() }()
			_, err = io.Copy(io.Discard, rc)
			return err
		})
		sparse := timeIt(t, func() error {
			rc, err := backend.Get(ctx, key)
			if err != nil {
				return err
			}
			defer func() { _ = rc.Close() }()
			dst, err := os.CreateTemp(dir, "sparse-*")
			if err != nil {
				return err
			}
			defer func() { _ = os.Remove(dst.Name()) }()
			defer func() { _ = dst.Close() }()
			if _, _, err := CopySparse(ctx, dst, rc); err != nil {
				return err
			}
			return dst.Sync()
		})
		fill := timeIt(t, func() error {
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
		t.Logf("rep %d: decode only %s | decode + sparse write + fsync %s | full cache fill %s",
			rep, decode.Round(time.Millisecond), sparse.Round(time.Millisecond), fill.Round(time.Millisecond))
	}

	// Prototype: compress only the data extents and record the holes, so a
	// miss decodes the drive's real bytes and leaves the rest as holes.
	var encoded bytes.Buffer
	var extents [][2]int64
	encode := timeIt(t, func() error {
		var err error
		extents, err = encodeDataExtents(src, &encoded)
		return err
	})
	t.Logf("extent prototype: %d extents, object %.1f MB, encode %s", len(extents), float64(encoded.Len())/1e6, encode.Round(time.Millisecond))
	for rep := 0; rep < 3; rep++ {
		restore := timeIt(t, func() error {
			dst, err := os.CreateTemp(dir, "extents-*")
			if err != nil {
				return err
			}
			defer func() { _ = os.Remove(dst.Name()) }()
			defer func() { _ = dst.Close() }()
			return decodeDataExtents(bytes.NewReader(encoded.Bytes()), extents, int64(logicalMiB)<<20, dst)
		})
		t.Logf("rep %d: extent decode + write + fsync %s", rep, restore.Round(time.Millisecond))
	}
}

// encodeDataExtents compresses src's data ranges (SEEK_DATA/SEEK_HOLE) into w
// as one zstd stream and returns the [offset, length] of each range.
func encodeDataExtents(path string, w io.Writer) ([][2]int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	enc, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	var extents [][2]int64
	for off := int64(0); off < info.Size(); {
		data, err := unix.Seek(int(f.Fd()), off, unix.SEEK_DATA)
		if err != nil {
			break // ENXIO: no data after off
		}
		hole, err := unix.Seek(int(f.Fd()), data, unix.SEEK_HOLE)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(enc, io.NewSectionReader(f, data, hole-data)); err != nil {
			return nil, err
		}
		extents = append(extents, [2]int64{data, hole - data})
		off = hole
	}
	return extents, enc.Close()
}

// decodeDataExtents writes each extent at its offset and sizes dst to the
// logical length, leaving holes everywhere else.
func decodeDataExtents(r io.Reader, extents [][2]int64, logical int64, dst *os.File) error {
	dec, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return err
	}
	defer dec.Close()
	for _, e := range extents {
		if _, err := io.CopyN(io.NewOffsetWriter(dst, e[0]), dec, e[1]); err != nil {
			return err
		}
	}
	if err := dst.Truncate(logical); err != nil {
		return err
	}
	return dst.Sync()
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
