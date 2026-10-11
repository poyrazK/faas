//go:build linux

package storage

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func randomPages(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n*pageSize)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func page(b []byte, i int) []byte { return b[i*pageSize : (i+1)*pageSize] }

// memoryFixture writes an image and a memory file whose pages are: a run of
// three consecutive image blocks, a single image block out of order, a zero
// page, and two pages found in no image.
func memoryFixture(t *testing.T, dir string) (imagePath, memPath string, mem []byte) {
	t.Helper()
	image := randomPages(t, 16)
	imagePath = filepath.Join(dir, "layer.ext4")
	if err := os.WriteFile(imagePath, image, 0o600); err != nil {
		t.Fatal(err)
	}
	other := randomPages(t, 2)
	mem = make([]byte, 0, 8*pageSize)
	mem = append(mem, page(image, 4)...)
	mem = append(mem, page(image, 5)...)
	mem = append(mem, page(image, 6)...)
	mem = append(mem, page(other, 0)...)
	mem = append(mem, page(image, 11)...)
	mem = append(mem, make([]byte, pageSize)...)
	mem = append(mem, page(other, 1)...)
	mem = append(mem, page(image, 0)...)
	memPath = filepath.Join(dir, "mem")
	if err := os.WriteFile(memPath, mem, 0o600); err != nil {
		t.Fatal(err)
	}
	return imagePath, memPath, mem
}

func TestIndexImageFilesSkipsZeroPages(t *testing.T) {
	dir := t.TempDir()
	image := append(randomPages(t, 2), make([]byte, 3*pageSize)...)
	path := filepath.Join(dir, "base.ext4")
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := indexImageFiles(t.Context(), []string{path, filepath.Join(dir, "evicted.ext4")})
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.locs) != 2 || len(idx.files) != 1 {
		t.Fatalf("index has %d blocks in %d files, want 2 in 1", len(idx.locs), len(idx.files))
	}
}

// Sharing must never change a byte of the memory file. Where the filesystem
// shares blocks (XFS, Btrfs) the five image pages are shared and nothing else.
func TestShareMemoryPagesKeepsBytesAndSharesImagePages(t *testing.T) {
	dir := t.TempDir()
	imagePath, memPath, want := memoryFixture(t, dir)
	idx, err := indexImageFiles(t.Context(), []string{imagePath})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := shareMemoryPages(t.Context(), memPath, idx)
	got, readErr := os.ReadFile(memPath)
	if readErr != nil || !bytes.Equal(got, want) {
		t.Fatalf("memory bytes changed (err=%v)", readErr)
	}
	if errors.Is(err, errDedupeUnsupported) {
		t.Skipf("%s cannot share blocks; byte identity still verified", dir)
	}
	if err != nil {
		t.Fatal(err)
	}
	if shared != 5*pageSize {
		t.Fatalf("shared %d bytes, want %d", shared, 5*pageSize)
	}
	extents, err := fileExtents(memPath)
	if err != nil {
		t.Fatal(err)
	}
	var sharedExtent int64
	for _, e := range extents {
		if e.shared {
			sharedExtent += e.length
		}
	}
	if sharedExtent != 5*pageSize {
		t.Fatalf("FIEMAP reports %d shared bytes, want %d", sharedExtent, 5*pageSize)
	}
}

// A memory file is processed once; the marker survives a new index.
func TestShareSnapshotMemoryRunsOncePerFile(t *testing.T) {
	dir := t.TempDir()
	parent, err := NewLocalStorageBackend(filepath.Join(dir, "parent"))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewLocalCacheBackend(parent, filepath.Join(dir, "cache"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	imagePath, memPath, _ := memoryFixture(t, dir)
	for key, path := range map[string]string{"apps/app/dep.ext4": imagePath, "snap/dep/captures/c/v2/mem": memPath} {
		f, err := os.Open(path) //nolint:forbidigo // test fixture under t.TempDir.
		if err != nil {
			t.Fatal(err)
		}
		err = cache.Put(t.Context(), key, f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	x := NewMemoryShareIndex()
	first, err := x.ShareSnapshotMemory(t.Context(), cache, "snap/dep/captures/c/v2/mem", []string{"apps/app/dep.ext4"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewMemoryShareIndex().ShareSnapshotMemory(t.Context(), cache, "snap/dep/captures/c/v2/mem", []string{"apps/app/dep.ext4"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Skipped {
		// No block sharing here: the pass is recorded in memory only.
		if again, _ := x.ShareSnapshotMemory(t.Context(), cache, "snap/dep/captures/c/v2/mem", nil); !again.Skipped {
			t.Fatal("unsupported filesystem pass repeated")
		}
		t.Skip("filesystem cannot share blocks")
	}
	if first.SharedBytes != 5*pageSize || !second.Skipped {
		t.Fatalf("first=%+v second=%+v, want 5 pages shared then skipped", first, second)
	}
	missing, err := x.ShareSnapshotMemory(t.Context(), cache, "snap/other/captures/c/v2/mem", nil)
	if err != nil || !missing.Skipped {
		t.Fatalf("uncached memory = %+v, %v; want skipped", missing, err)
	}
}
