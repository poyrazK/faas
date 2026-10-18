// adr: 1135
package storage

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const extentTestKey = "apps/demo/drive.ext4"

// writeHoledFile creates a size-byte file with random data at each offset and
// holes elsewhere, and returns its full contents.
func writeHoledFile(t *testing.T, path string, size int64, chunks map[int64]int) []byte {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	want := make([]byte, size)
	for off, n := range chunks {
		buf := make([]byte, n)
		if _, err := rand.Read(buf); err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteAt(buf, off); err != nil {
			t.Fatal(err)
		}
		copy(want[off:], buf)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	return want
}

func putFile(t *testing.T, backend *GCSStorageBackend, key, path string) {
	t.Helper()
	f, err := os.Open(path) //nolint:forbidigo // test fixture under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := backend.Put(context.Background(), key, f); err != nil {
		t.Fatalf("put: %v", err)
	}
}

func newExtentBackend(t *testing.T, sparse bool) (*GCSStorageBackend, *memoryGCSStore) {
	t.Helper()
	store := newMemoryGCSStore()
	backend, err := newGCSStorageBackend("bucket", snapshotCompressionZstd, store)
	if err != nil {
		t.Fatal(err)
	}
	return backend.WithSparseExtents(sparse), store
}

func TestExtentEncodingRoundTrip(t *testing.T) {
	const mib = 1 << 20
	for _, tc := range []struct {
		name   string
		size   int64
		chunks map[int64]int
	}{
		{name: "holes around data", size: 16 * mib, chunks: map[int64]int{0: 4096, 3 * mib: 64 * 1024, 9*mib + 4096: mib}},
		{name: "data at the very end", size: 4 * mib, chunks: map[int64]int{4*mib - 8192: 8192}},
		{name: "dense", size: 256 * 1024, chunks: map[int64]int{0: 256 * 1024}},
		{name: "all hole", size: 2 * mib, chunks: map[int64]int{}},
		{name: "empty", size: 0, chunks: map[int64]int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "src")
			want := writeHoledFile(t, src, tc.size, tc.chunks)
			backend, store := newExtentBackend(t, true)
			putFile(t, backend, extentTestKey, src)
			if enc := store.objects["bucket/"+extentTestKey].metadata[gcsEncodingMetadata]; enc != encodingZstdExtents {
				t.Fatalf("encoding = %q, want %q", enc, encodingZstdExtents)
			}

			// Streaming readers see the logical bytes.
			rc, err := backend.Get(context.Background(), extentTestKey)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("streamed %d bytes (err %v), want the original %d", len(got), err, len(want))
			}

			// The sparse fast path writes the same bytes.
			rc, err = backend.Get(context.Background(), extentTestKey)
			if err != nil {
				t.Fatal(err)
			}
			dst, err := os.Create(filepath.Join(dir, "dst"))
			if err != nil {
				t.Fatal(err)
			}
			n, err := copyArtifactContext(context.Background(), dst, rc, extentTestKey)
			_ = rc.Close()
			_ = dst.Close()
			if err != nil || n != tc.size {
				t.Fatalf("sparse write = %d, %v; want %d", n, err, tc.size)
			}
			if got, _ := os.ReadFile(dst.Name()); !bytes.Equal(got, want) {
				t.Fatal("sparse write produced different bytes")
			}
		})
	}
}

// A stream that is not a file has no holes to list, and the writer opt-in
// gates the format: both keep the plain zstd encoding.
func TestExtentEncodingFallsBackToZstd(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	want := writeHoledFile(t, src, 1<<20, map[int64]int{4096: 4096})
	for _, tc := range []struct {
		name   string
		sparse bool
		reader func() io.Reader
	}{
		{name: "writer opt-in off", sparse: false},
		{name: "stream, not a file", sparse: true, reader: func() io.Reader { return bytes.NewReader(want) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, store := newExtentBackend(t, tc.sparse)
			if tc.reader == nil {
				putFile(t, backend, extentTestKey, src)
			} else if err := backend.Put(context.Background(), extentTestKey, tc.reader()); err != nil {
				t.Fatal(err)
			}
			if enc := store.objects["bucket/"+extentTestKey].metadata[gcsEncodingMetadata]; enc != snapshotCompressionZstd {
				t.Fatalf("encoding = %q, want %q", enc, snapshotCompressionZstd)
			}
			rc, err := backend.Get(context.Background(), extentTestKey)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rc.Close() }()
			if got, _ := io.ReadAll(rc); !bytes.Equal(got, want) {
				t.Fatal("round trip changed the bytes")
			}
		})
	}
}

func TestExtentEncodingRejectsCorruption(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	writeHoledFile(t, src, 1<<20, map[int64]int{8192: 8192})
	backend, store := newExtentBackend(t, true)
	putFile(t, backend, extentTestKey, src)
	object := store.objects["bucket/"+extentTestKey]

	tamper := func(t *testing.T, edit func(o *memoryGCSObject)) error {
		t.Helper()
		o := memoryGCSObject{body: append([]byte(nil), object.body...), metadata: map[string]string{}, generation: 1}
		for k, v := range object.metadata {
			o.metadata[k] = v
		}
		edit(&o)
		store.objects["bucket/"+extentTestKey] = o
		rc, err := backend.Get(context.Background(), extentTestKey)
		if err != nil {
			return err
		}
		defer func() { _ = rc.Close() }()
		_, err = io.ReadAll(rc)
		return err
	}
	t.Run("logical size disagrees with metadata", func(t *testing.T) {
		err := tamper(t, func(o *memoryGCSObject) { o.metadata[gcsUncompressedSizeMetadata] = "4096" })
		if !errors.Is(err, errCorruptExtentStream) {
			t.Fatalf("err = %v, want corrupt extent stream", err)
		}
	})
	t.Run("truncated object", func(t *testing.T) {
		if err := tamper(t, func(o *memoryGCSObject) { o.body = o.body[:len(o.body)/2] }); err == nil {
			t.Fatal("a truncated object decoded without error")
		}
	})
	t.Run("not an extent stream", func(t *testing.T) {
		plain, _ := newExtentBackend(t, false)
		f, _ := os.Open(src) //nolint:forbidigo // test fixture under t.TempDir.
		defer func() { _ = f.Close() }()
		if err := plain.Put(context.Background(), "apps/demo/plain.ext4", f); err != nil {
			t.Fatal(err)
		}
		err := tamper(t, func(o *memoryGCSObject) { o.body = store.objects["bucket/apps/demo/plain.ext4"].body })
		if !errors.Is(err, errCorruptExtentStream) {
			t.Fatalf("err = %v, want corrupt extent stream", err)
		}
	})
}

func TestReadExtentHeaderBounds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		size    int64
		extents []dataExtent
	}{
		{name: "extent past the logical size", size: 4096, extents: []dataExtent{{off: 0, length: 8192}}},
		{name: "zero-length extent", size: 4096, extents: []dataExtent{{off: 0, length: 0}}},
	} {
		header := appendExtentHeader(nil, tc.size, tc.extents)
		if _, _, err := readExtentHeader(bufioReader(header)); !errors.Is(err, errCorruptExtentStream) {
			t.Errorf("%s: err = %v, want corrupt extent stream", tc.name, err)
		}
	}
}

func bufioReader(b []byte) *bufio.Reader { return bufio.NewReader(bytes.NewReader(b)) }

// A node-cache miss of an extent-encoded drive stays sparse on disk.
func TestCacheFillOfExtentEncodedDriveStaysSparse(t *testing.T) {
	const size = 64 << 20
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	want := writeHoledFile(t, src, size, map[int64]int{0: 1 << 20, 32 << 20: 1 << 20})
	f, err := os.Open(src) //nolint:forbidigo // test fixture under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	extents, err := fileDataExtents(f, size)
	_ = f.Close()
	if err != nil || len(extents) < 2 {
		t.Skipf("this filesystem does not report holes (%d extents, %v)", len(extents), err)
	}
	backend, _ := newExtentBackend(t, true)
	putFile(t, backend, extentTestKey, src)
	cache, err := NewLocalCacheBackend(backend, filepath.Join(dir, "cache"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := cache.Get(context.Background(), extentTestKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("cache fill changed the bytes")
	}
	path, _ := cache.cacheFileFor(extentTestKey)
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatal(err)
	}
	if allocated := st.Blocks * 512; allocated > size/4 {
		t.Fatalf("cache entry allocates %d bytes of a %d-byte drive with 2 MiB of data; want it sparse", allocated, size)
	}
}

func TestSparseExtentsFromEnv(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "0": false, "false": false, "1": true, "TRUE": true} {
		t.Setenv("FAAS_STORAGE_SPARSE_EXTENTS", raw)
		if got, err := sparseExtentsFromEnv(); err != nil || got != want {
			t.Errorf("%q: got %v, %v; want %v", raw, got, err, want)
		}
	}
	t.Setenv("FAAS_STORAGE_SPARSE_EXTENTS", "yes")
	if _, err := sparseExtentsFromEnv(); err == nil || !strings.Contains(err.Error(), "FAAS_STORAGE_SPARSE_EXTENTS") {
		t.Errorf("an unknown value was accepted: %v", err)
	}
}
