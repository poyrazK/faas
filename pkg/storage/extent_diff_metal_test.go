//go:build metal && linux

package storage

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// adr: 20261122132055 §4 — on the node's XFS, two reflink clones of a volume
// taken around one guest write differ in exactly the blocks it wrote.
func TestMetalChangedRangesBetweenClones(t *testing.T) {
	const size = 16 << 20
	dir := metalReflinkDir(t)
	vol := filepath.Join(dir, "vol.ext4")
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vol, data, 0o600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "base")
	if cloned, err := CloneFile(base, vol); err != nil || !cloned {
		t.Skipf("%s cannot reflink (cloned %v): %v", dir, cloned, err)
	}
	f, err := os.OpenFile(vol, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(make([]byte, 4096), 5<<20); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	next := filepath.Join(dir, "next")
	if _, err := CloneFile(next, vol); err != nil {
		t.Fatal(err)
	}
	changed, err := ChangedRanges(base, next)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Range{{Start: 5 << 20, End: 5<<20 + 4096}}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed = %v, want %v", changed, want)
	}
	same, err := ChangedRanges(next, next)
	if err != nil || len(same) != 0 {
		t.Fatalf("a clone against itself changed %v (%v)", same, err)
	}
}
