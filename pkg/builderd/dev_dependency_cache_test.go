package builderd

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDependencyCacheKeyForAppIsTenantAndAppScoped(t *testing.T) {
	dev := state.App{
		ID:              "app-a",
		AccountID:       "account-a",
		PreviewOfSlug:   "project",
		PreviewPrNumber: 0,
	}
	base := dependencyCacheKeyForApp(dev, FrameworkNode, "apps/api", "runner-a")
	if len(base) != 64 {
		t.Fatalf("developer key length = %d, want 64", len(base))
	}
	if root := dependencyCacheKeyForApp(dev, FrameworkNode, ".", "runner-a"); root != dependencyCacheKeyForApp(dev, FrameworkNode, "", "runner-a") {
		t.Fatalf("archive-root spellings produced different keys: dot=%q empty=%q", root, dependencyCacheKeyForApp(dev, FrameworkNode, "", "runner-a"))
	}
	variants := []struct {
		name string
		app  state.App
		fw   Framework
		root string
		base string
	}{
		{name: "account", app: state.App{ID: "app-a", AccountID: "account-b", PreviewOfSlug: "project"}, fw: FrameworkNode, root: "apps/api", base: "runner-a"},
		{name: "workspace app", app: state.App{ID: "app-b", AccountID: "account-a", PreviewOfSlug: "project"}, fw: FrameworkNode, root: "apps/api", base: "runner-a"},
		{name: "member", app: dev, fw: FrameworkNode, root: "apps/web", base: "runner-a"},
		{name: "framework", app: dev, fw: FrameworkPython, root: "apps/api", base: "runner-a"},
		{name: "dockerfile", app: dev, fw: FrameworkDocker, root: "apps/api", base: "runner-a"},
		{name: "runtime base", app: dev, fw: FrameworkNode, root: "apps/api", base: "runner-b"},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			got := dependencyCacheKeyForApp(variant.app, variant.fw, variant.root, variant.base)
			if got == "" || got == base {
				t.Fatalf("key = %q, want non-empty value isolated from %q", got, base)
			}
		})
	}

	production := state.App{ID: "production-app", AccountID: "account-a"}
	if got := dependencyCacheKeyForApp(production, FrameworkNode, "apps/api", "runner-a"); len(got) != 64 || got == base {
		t.Fatalf("production key = %q, want app-isolated 64-character key", got)
	}
	preview := dev
	preview.PreviewPrNumber = 42
	if got := dependencyCacheKeyForApp(preview, FrameworkNode, "", "runner-a"); got != "" {
		t.Fatalf("pull-request preview key = %q, want disabled", got)
	}
	if got := dependencyCacheKeyForApp(dev, FrameworkDocker, "", "runner-a"); got == "" || got == base {
		t.Fatalf("Dockerfile key = %q, want a developer-scoped key distinct from Railpack", got)
	}
}

func TestPublishDependencyCacheReplacesAtomically(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "export")
	writeCacheFixture(t, src, "first")
	sourceInfo, err := os.Stat(filepath.Join(src, "blobs", "sha256", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "stable")
	if err := publishDependencyCache(src, dst, 1<<20); err != nil {
		t.Fatal(err)
	}
	assertCacheFixture(t, dst, "first")
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("same-filesystem source remains after publish: %v", err)
	}
	publishedInfo, err := os.Stat(filepath.Join(dst, "blobs", "sha256", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(sourceInfo, publishedInfo) {
		t.Fatal("same-filesystem publish copied the cache instead of moving it")
	}

	writeCacheFixture(t, src, "second")
	if err := publishDependencyCache(src, dst, 1<<20); err != nil {
		t.Fatal(err)
	}
	assertCacheFixture(t, dst, "second")
	if _, err := os.Stat(dst + ".previous"); !os.IsNotExist(err) {
		t.Fatalf("previous generation remains after publish: %v", err)
	}
}

func TestStageDependencyCacheFallsBackToCopyAcrossFilesystems(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "export")
	staged := filepath.Join(root, "staged")
	writeCacheFixture(t, src, "cross-device")

	err := stageDependencyCache(src, staged, 1<<20, func(string, string) error {
		return syscall.EXDEV
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCacheFixture(t, staged, "cross-device")
	assertCacheFixture(t, src, "cross-device")
}

func TestStageDependencyCacheValidatesBeforeMove(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "export")
	writeCacheFixture(t, src, "safe")
	if err := os.Symlink("index.json", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	renameCalled := false
	err := stageDependencyCache(src, filepath.Join(root, "staged"), 1<<20, func(string, string) error {
		renameCalled = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported entry") {
		t.Fatalf("error = %v, want unsupported entry", err)
	}
	if renameCalled {
		t.Fatal("invalid cache reached rename")
	}
}

func TestStageDependencyCacheForDriveUsesHardLinks(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "cache")
	dst := filepath.Join(root, "staged")
	writeCacheFixture(t, src, "linked")

	if err := stageDependencyCacheForDrive(src, dst, 1<<20, os.Link); err != nil {
		t.Fatal(err)
	}
	assertCacheFixture(t, dst, "linked")
	sourceInfo, err := os.Stat(filepath.Join(src, "blobs", "sha256", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	stagedInfo, err := os.Stat(filepath.Join(dst, "blobs", "sha256", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(sourceInfo, stagedInfo) {
		t.Fatal("same-filesystem drive staging copied the cache instead of linking it")
	}
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	assertCacheFixture(t, dst, "linked")
}

func TestStageDependencyCacheForDriveFallsBackToCopy(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "cache")
	dst := filepath.Join(root, "staged")
	writeCacheFixture(t, src, "copied")

	if err := stageDependencyCacheForDrive(src, dst, 1<<20, func(string, string) error {
		return syscall.EXDEV
	}); err != nil {
		t.Fatal(err)
	}
	assertCacheFixture(t, dst, "copied")
	sourceInfo, err := os.Stat(filepath.Join(src, "blobs", "sha256", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	stagedInfo, err := os.Stat(filepath.Join(dst, "blobs", "sha256", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(sourceInfo, stagedInfo) {
		t.Fatal("cross-filesystem fallback unexpectedly retained a hard link")
	}
}

func TestCopyDependencyCacheRejectsUnsafeOrOversizedEntries(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "cache")
		writeCacheFixture(t, src, "too-large")
		if err := copyDependencyCache(src, filepath.Join(t.TempDir(), "out"), 3); err == nil || !strings.Contains(err.Error(), "ceiling") {
			t.Fatalf("error = %v, want byte ceiling", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "cache")
		writeCacheFixture(t, src, "safe")
		if err := os.Symlink("index.json", filepath.Join(src, "link")); err != nil {
			t.Fatal(err)
		}
		if err := copyDependencyCache(src, filepath.Join(t.TempDir(), "out"), 1<<20); err == nil || !strings.Contains(err.Error(), "unsupported entry") {
			t.Fatalf("error = %v, want unsupported entry", err)
		}
	})
}

func TestSweepDependencyCachesRemovesOnlyStaleGeneratedDirectories(t *testing.T) {
	driveDir := t.TempDir()
	root := filepath.Join(driveDir, devDependencyCacheDir)
	stale := filepath.Join(root, strings.Repeat("a", 64))
	recent := filepath.Join(root, strings.Repeat("b", 64))
	unrelated := filepath.Join(root, "operator-notes")
	for _, path := range []string{stale, recent, unrelated} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	old := now.Add(-devDependencyCacheTTL - time.Hour)
	for _, path := range []string{stale, unrelated} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := sweepDependencyCaches(driveDir, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale generated cache remains: %v", err)
	}
	for _, path := range []string{recent, unrelated} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("preserved directory %s: %v", filepath.Base(path), err)
		}
	}
}

func TestSweepDependencyCachesEvictsOldestAboveSizeBudget(t *testing.T) {
	driveDir := t.TempDir()
	root := filepath.Join(driveDir, devDependencyCacheDir)
	oldest := filepath.Join(root, strings.Repeat("c", 64))
	newest := filepath.Join(root, strings.Repeat("d", 64))
	writeCacheFixture(t, oldest, "12345")
	writeCacheFixture(t, newest, "67890")
	now := time.Now()
	oldStamp := now.Add(-2 * time.Hour)
	newStamp := now.Add(-time.Hour)
	if err := os.Chtimes(oldest, oldStamp, oldStamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newest, newStamp, newStamp); err != nil {
		t.Fatal(err)
	}
	newestSize, err := dependencyCacheSize(newest)
	if err != nil {
		t.Fatal(err)
	}
	if err := sweepDependencyCachesWithLimit(driveDir, now, newestSize); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldest); !os.IsNotExist(err) {
		t.Fatalf("oldest cache remains above budget: %v", err)
	}
	if _, err := os.Stat(newest); err != nil {
		t.Fatalf("newest cache was evicted first: %v", err)
	}
}

func TestDependencyCachePathRejectsRawPathInput(t *testing.T) {
	if _, err := dependencyCachePath(t.TempDir(), "../outside"); err == nil {
		t.Fatal("path-shaped cache key accepted")
	}
}

func writeCacheFixture(t *testing.T, root, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "blobs", "sha256"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), []byte(`{"schemaVersion":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "blobs", "sha256", "fixture"), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertCacheFixture(t *testing.T, root, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, "blobs", "sha256", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("cache payload = %q, want %q", got, want)
	}
}
