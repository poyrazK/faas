package releasebundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// overlayFixture builds a base release and a hard-linked copy of it, the
// way the control plane assembles a per-unit release.
func overlayFixture(t *testing.T) (Manifest, string, string) {
	t.Helper()
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "bin", "apid"), "apid-base", 0o755)
	writeFile(t, filepath.Join(base, "bin", "schedd"), "schedd-base", 0o755)
	writeFile(t, filepath.Join(base, "systemd", "faas-apid.service"), "[Service]\n", 0o644)
	manifest, err := Build(base, "base", "commit-base", "linux/amd64", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(base, manifest); err != nil {
		t.Fatal(err)
	}
	overlay := t.TempDir()
	for _, file := range manifest.Files {
		dst := filepath.Join(overlay, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(filepath.Join(base, filepath.FromSlash(file.Path)), dst); err != nil {
			t.Fatal(err)
		}
	}
	return manifest, base, overlay
}

func replaceFile(t *testing.T, root, rel, body string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	// Unlink first: writing through the hard link would change the base.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	writeFile(t, path, body, mode)
}

func TestOverlayHashesReplacedFilesAndInheritsTheRest(t *testing.T) {
	base, baseRoot, root := overlayFixture(t)
	replaceFile(t, root, "bin/apid", "apid-unit", 0o755)
	writeFile(t, filepath.Join(root, "unit-versions.json"), "{}\n", 0o644)

	manifest, err := Overlay(base, root, "overlay", "commit-unit", []string{"bin/apid", "unit-versions.json"}, time.Now())
	if err != nil {
		t.Fatalf("Overlay: %v", err)
	}
	if manifest.ReleaseID != "overlay" || manifest.CommitSHA != "commit-unit" || manifest.Target != base.Target {
		t.Fatalf("identity = %+v", manifest)
	}
	if err := Write(root, manifest); err != nil {
		t.Fatal(err)
	}
	// The controller's full verification must accept what Overlay wrote.
	if err := Verify(root, manifest); err != nil {
		t.Fatalf("Verify overlay: %v", err)
	}
	byPath := map[string]File{}
	for _, file := range manifest.Files {
		byPath[file.Path] = file
	}
	if byPath["bin/schedd"] != base.Files[1] {
		t.Fatalf("schedd entry = %+v, want inherited %+v", byPath["bin/schedd"], base.Files[1])
	}
	if byPath["bin/apid"].SHA256 == base.Files[0].SHA256 {
		t.Fatal("replaced apid kept the base hash")
	}
	// The base release is untouched.
	if err := Verify(baseRoot, base); err != nil {
		t.Fatalf("base release changed: %v", err)
	}
}

func TestOverlayRejects(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func(t *testing.T, root string)
		id       string
		replaced []string
		want     string
	}{
		{name: "reused release id", id: "base", replaced: []string{"bin/apid"}, want: "reuse base release id"},
		{name: "nothing replaced", id: "overlay", want: "replaces no files"},
		{name: "replaced twice", id: "overlay", replaced: []string{"bin/apid", "bin/apid"}, want: "twice"},
		{name: "manifest", id: "overlay", replaced: []string{ManifestName}, want: "cannot replace"},
		{name: "unsafe path", id: "overlay", replaced: []string{"../bin/apid"}, want: "unsafe path"},
		{name: "missing replacement", id: "overlay", replaced: []string{"bin/meterd"}, want: "overlay file bin/meterd"},
		{
			name: "symlinked replacement", id: "overlay", replaced: []string{"bin/apid"}, want: "not a regular file",
			mutate: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "bin", "apid")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/bin/sh", filepath.Join(root, "bin", "apid")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unlisted extra file", id: "overlay", replaced: []string{"bin/apid"}, want: "does not: bin/extra",
			mutate: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, "bin", "extra"), "x", 0o755) },
		},
		{
			name: "drifted inherited file", id: "overlay", replaced: []string{"bin/apid"}, want: "bin/schedd differs",
			mutate: func(t *testing.T, root string) { replaceFile(t, root, "bin/schedd", "schedd-drifted", 0o755) },
		},
		{
			name: "inherited mode change", id: "overlay", replaced: []string{"bin/apid"}, want: "differs",
			mutate: func(t *testing.T, root string) {
				if err := os.Chmod(filepath.Join(root, "systemd", "faas-apid.service"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "deleted base file", id: "overlay", replaced: []string{"bin/apid"}, want: "missing base files: bin/schedd",
			mutate: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "bin", "schedd")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, _, root := overlayFixture(t)
			replaceFile(t, root, "bin/apid", "apid-unit", 0o755)
			if tc.mutate != nil {
				tc.mutate(t, root)
			}
			_, err := Overlay(base, root, tc.id, "commit-unit", tc.replaced, time.Now())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Overlay error = %v, want %q", err, tc.want)
			}
		})
	}
}
