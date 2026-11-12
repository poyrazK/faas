package rootfs

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// File names that merely contain two dots are ordinary names. A Debian man
// page ships as usr/share/man/man1/..1.gz; matching the ".." substring
// rejected every image built on such a base (Hunt 34 H34-47).
var dottedNames = []string{
	"usr/share/man/man1/..1.gz",
	"a..b/c",
	"...",
	"foo/..bar",
	"bar../baz",
}

func TestLayerAcceptsNamesContainingTwoDots(t *testing.T) {
	entries := make([]entry, 0, len(dottedNames))
	for _, name := range dottedNames {
		entries = append(entries, entry{name: name, body: "x"})
	}
	staging := t.TempDir()
	if err := applyLayerGzForApp(staging, gzLayer(t, entries), newExtractionBudget(1<<20)); err != nil {
		t.Fatalf("layer with dotted file names rejected: %v", err)
	}
	for _, name := range dottedNames {
		if _, err := os.Stat(filepath.Join(staging, filepath.FromSlash(name))); err != nil {
			t.Fatalf("%s not extracted: %v", name, err)
		}
	}
}

func TestFunctionTarballAcceptsNamesContainingTwoDots(t *testing.T) {
	entries := make([]entry, 0, len(dottedNames))
	for _, name := range dottedNames {
		entries = append(entries, entry{name: name, body: "x"})
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, gzLayer(t, entries)); err != nil {
		t.Fatal(err)
	}
	app := t.TempDir()
	if err := applyTarballWithCap(app, &buf, 1<<20, ""); err != nil {
		t.Fatalf("function tarball with dotted file names rejected: %v", err)
	}
	for _, name := range dottedNames {
		if _, err := os.Stat(filepath.Join(app, filepath.FromSlash(name))); err != nil {
			t.Fatalf("%s not extracted: %v", name, err)
		}
	}
}

// A ".." component still climbs, wherever it sits in the name.
func TestLayerRejectsParentComponents(t *testing.T) {
	for _, name := range []string{"../escape", "a/../../escape", "a/../b", "a/..", "./../x"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			staging := filepath.Join(root, "staging")
			if err := os.MkdirAll(staging, 0o755); err != nil {
				t.Fatal(err)
			}
			err := applyLayerGzForApp(staging, gzLayer(t, []entry{{name: name, body: "x"}}), newExtractionBudget(1<<20))
			if err == nil {
				t.Fatalf("entry %q with a parent component was accepted", name)
			}
			if _, statErr := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(statErr) {
				t.Fatalf("entry written outside staging: %v", statErr)
			}
		})
	}
}

func TestHasParentComponent(t *testing.T) {
	for name, want := range map[string]bool{
		"..":                        true,
		"../x":                      true,
		"a/..":                      true,
		"a/../b":                    true,
		"usr/share/man/man1/..1.gz": false,
		"...":                       false,
		"a..b":                      false,
		".wh..wh..opq":              false,
		"":                          false,
	} {
		if got := hasParentComponent(name); got != want {
			t.Errorf("hasParentComponent(%q) = %v, want %v", name, got, want)
		}
	}
}
