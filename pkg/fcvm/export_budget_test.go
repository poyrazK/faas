package fcvm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The build writes the drive vmmd exports from. Sparse files and hard links
// take no room there but are copied at full size, so the export is held to a
// total byte and entry budget, not only a per-file cap.
func TestMoveTreeHoldsTheWholeExportToItsBudget(t *testing.T) {
	// moveTree consumes its source, so every call gets a fresh one.
	src := func() string {
		dir := t.TempDir()
		for _, name := range []string{"a", "b", "c"} {
			f, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Truncate(1 << 20); err != nil { // sparse: 1 MiB apparent, no blocks
				t.Fatal(err)
			}
			_ = f.Close()
		}
		if err := os.Link(filepath.Join(dir, "a"), filepath.Join(dir, "a-link")); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	err := moveTreeWithin(src(), filepath.Join(t.TempDir(), "out"), 1<<20, 2<<20, 100)
	if !errors.Is(err, errExportBudget) {
		t.Fatalf("export of 4 MiB apparent under a 2 MiB budget = %v, want errExportBudget", err)
	}
	if err := moveTreeWithin(src(), filepath.Join(t.TempDir(), "out"), 1<<20, 4<<20, 100); err != nil {
		t.Fatalf("export within the budget: %v", err)
	}
	if err := moveTreeWithin(src(), filepath.Join(t.TempDir(), "out"), 1<<20, 4<<20, 3); !errors.Is(err, errExportBudget) {
		t.Fatalf("export past the entry budget = %v, want errExportBudget", err)
	}
	if api.MaxExportedTreeBytes < api.MaxExportedLayerBytes || api.MaxExportedTreeEntries < 1 {
		t.Fatal("export tree budget below the per-file cap")
	}
}
