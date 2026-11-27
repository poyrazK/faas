// adr: 20261029171525 amendment 2 — vmmd, not the host's builderd, turns a
// deferred build's drive into its cache export run's.
package fcvm

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestStageCacheExportRunReplacesTheBuildsInputs(t *testing.T) {
	dir := t.TempDir()
	put := func(p, data string) {
		t.Helper()
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put(cacheExportManifestPath, `{"defer_cache_export":true}`)
	put(cacheExportEntropyPath, "the build's seed")
	put(cacheExportDonePath, `{"exit_code":0}`)
	put("upper/var/lib/buildkit/state.db", "the build's BuildKit state")
	img := &dirDriveImage{root: dir}
	prev := openDriveImage
	openDriveImage = func(*JailerVMM, string) (driveImage, error) { return img, nil }
	t.Cleanup(func() { openDriveImage = prev })

	const instance = "build-1-cache"
	v := NewJailerVMM(t.TempDir(), 0)
	if err := os.MkdirAll(v.chrootRoot(instance), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.chrootRoot(instance), layerImageName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"dependency_cache":true,"cache_export_only":true}`)
	if err := v.stageCacheExportRunForOwner(context.Background(), nativeLaunchRecord{}, instance, manifest); err != nil {
		t.Fatal(err)
	}
	read := func(p string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if got := read(cacheExportManifestPath); !bytes.Equal(got, manifest) {
		t.Fatalf("build.json = %s, want the run's manifest", got)
	}
	if seed := read(cacheExportEntropyPath); len(seed) != cacheExportEntropyBytes || string(seed) == "the build's seed" {
		t.Fatalf("entropy seed = %d bytes, want a fresh %d-byte seed", len(seed), cacheExportEntropyBytes)
	}
	if _, err := os.Stat(filepath.Join(dir, cacheExportDonePath)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the build's build-done.json survived: %v", err)
	}
	if got := read("upper/var/lib/buildkit/state.db"); string(got) != "the build's BuildKit state" {
		t.Fatal("staging touched the build's BuildKit state")
	}
	if img.prepared != 1 {
		t.Fatalf("journal replays = %d, want 1 before the edit", img.prepared)
	}
}

func TestStageCacheExportRunNeedsTheDrive(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), 0)
	err := v.stageCacheExportRunForOwner(context.Background(), nativeLaunchRecord{}, "build-1-cache", []byte(`{}`))
	if err == nil {
		t.Fatal("staged a run with no drive1")
	}
}
