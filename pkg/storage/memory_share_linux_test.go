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

// minRunPages is the shortest run the pass shares.
const minRunPages = memoryShareMinRun / pageSize

// memoryFixture writes an image and a memory file whose pages are: a
// minimum-length run of consecutive image blocks, a page found in no image,
// a three-block run, a zero page, another unknown page and a single image
// block. Only the first run is long enough to share.
func memoryFixture(t *testing.T, dir string) (imagePath, memPath string, mem []byte) {
	t.Helper()
	image := randomPages(t, 40)
	imagePath = filepath.Join(dir, "layer.ext4")
	if err := os.WriteFile(imagePath, image, 0o600); err != nil {
		t.Fatal(err)
	}
	other := randomPages(t, 2)
	mem = append([]byte{}, image[4*pageSize:(4+minRunPages)*pageSize]...)
	mem = append(mem, page(other, 0)...)
	mem = append(mem, image[30*pageSize:33*pageSize]...)
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
// shares blocks (XFS, Btrfs) the minimum-length run is shared and the shorter
// matches are not.
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
	if shared != memoryShareMinRun {
		t.Fatalf("shared %d bytes, want %d", shared, memoryShareMinRun)
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
	if sharedExtent != memoryShareMinRun {
		t.Fatalf("FIEMAP reports %d shared bytes, want %d", sharedExtent, memoryShareMinRun)
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
	if first.SharedBytes != memoryShareMinRun || !second.Skipped {
		t.Fatalf("first=%+v second=%+v, want one minimum run shared then skipped", first, second)
	}
	missing, err := x.ShareSnapshotMemory(t.Context(), cache, "snap/other/captures/c/v2/mem", nil)
	if err != nil || !missing.Skipped {
		t.Fatalf("uncached memory = %+v, %v; want skipped", missing, err)
	}
}

// A block that changed after indexing fails the kernel's comparison; the run
// is retried in minimum-length chunks, so only the chunk holding the changed
// block stays unshared and no single pages are shared.
func TestShareMemoryPagesRetriesChangedRunInChunks(t *testing.T) {
	dir := t.TempDir()
	image := randomPages(t, 2*minRunPages)
	imagePath := filepath.Join(dir, "layer.ext4")
	if err := os.WriteFile(imagePath, image, 0o600); err != nil {
		t.Fatal(err)
	}
	memPath := filepath.Join(dir, "mem")
	if err := os.WriteFile(memPath, image, 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := indexImageFiles(t.Context(), []string{imagePath})
	if err != nil {
		t.Fatal(err)
	}
	changed := append([]byte{}, image...)
	copy(page(changed, minRunPages+3), randomPages(t, 1))
	if err := os.WriteFile(imagePath, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	shared, err := shareMemoryPages(t.Context(), memPath, idx)
	if errors.Is(err, errDedupeUnsupported) {
		t.Skipf("%s cannot share blocks", dir)
	}
	if err != nil {
		t.Fatal(err)
	}
	if shared != memoryShareMinRun {
		t.Fatalf("shared %d bytes, want the unchanged chunk (%d)", shared, memoryShareMinRun)
	}
}
