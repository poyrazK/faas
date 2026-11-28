//go:build linux

package fcvm

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// adr: 20261122132055 — a merged Diff equals the guest memory: parent pages
// where the diff has a hole, the diff's pages where it has data, and a page
// the guest zeroed reads back as zeros.
func TestMergeDiffSnapshot(t *testing.T) {
	const page = 4096
	const pages = 64
	dir := t.TempDir()
	parentPath := filepath.Join(dir, "parent")
	diffPath := filepath.Join(dir, "diff")
	dst := filepath.Join(dir, "merged")

	parent := bytes.Repeat([]byte{0}, pages*page)
	for i := range pages {
		if i%3 != 0 { // leave some parent pages as zeros (holes in a sparse image)
			fill(parent[i*page:(i+1)*page], byte('a'+i%26))
		}
	}
	if err := os.WriteFile(parentPath, parent, 0o600); err != nil {
		t.Fatal(err)
	}
	// The diff is sparse: data only at dirtied pages, one of them zeroed.
	diff, err := os.Create(diffPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := diff.Truncate(pages * page); err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), parent...)
	for _, dirty := range []struct {
		page int
		b    byte
	}{{1, 'X'}, {2, 'Y'}, {3, 'Z'}, {40, 0}, {63, 'Q'}} {
		buf := make([]byte, page)
		fill(buf, dirty.b)
		if _, err := diff.WriteAt(buf, int64(dirty.page*page)); err != nil {
			t.Fatal(err)
		}
		copy(want[dirty.page*page:], buf)
	}
	if err := diff.Close(); err != nil {
		t.Fatal(err)
	}

	pf, err := os.Open(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pf.Close() }()
	size, _, err := mergeDiffSnapshot(context.Background(), pf, diffPath, dst)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if size != pages*page {
		t.Fatalf("size %d, want %d", size, pages*page)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		for i := range pages {
			if !bytes.Equal(got[i*page:(i+1)*page], want[i*page:(i+1)*page]) {
				t.Fatalf("page %d differs: got %q…, want %q…", i, got[i*page], want[i*page])
			}
		}
	}
	// The parent is untouched: it may still back a running guest.
	if again, _ := os.ReadFile(parentPath); !bytes.Equal(again, parent) {
		t.Fatal("merge modified the parent")
	}
}

// adr: 20261122132055 — a diff of another size is refused, not merged.
func TestMergeDiffSnapshotSizeMismatch(t *testing.T) {
	dir := t.TempDir()
	parentPath, diffPath := filepath.Join(dir, "parent"), filepath.Join(dir, "diff")
	if err := os.WriteFile(parentPath, make([]byte, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(diffPath, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	pf, err := os.Open(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pf.Close() }()
	dst := filepath.Join(dir, "merged")
	if _, _, err := mergeDiffSnapshot(context.Background(), pf, diffPath, dst); err == nil {
		t.Fatal("size mismatch merged")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("failed merge left %s: %v", dst, err)
	}
}

func fill(b []byte, v byte) {
	for i := range b {
		b[i] = v
	}
}
