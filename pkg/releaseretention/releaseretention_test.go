package releaseretention

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneKeepsCurrentAndNewestPrevious(t *testing.T) {
	root := t.TempDir()
	releases := filepath.Join(root, "releases")
	if err := os.MkdirAll(releases, 0o755); err != nil {
		t.Fatal(err)
	}
	ids := []string{
		"0000000000000000000000000000000000000001",
		"0000000000000000000000000000000000000002",
		"0000000000000000000000000000000000000003",
		"0000000000000000000000000000000000000004",
		"0000000000000000000000000000000000000005",
	}
	for i, id := range ids {
		path := filepath.Join(releases, id)
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		when := time.Unix(int64(i+1), 0)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	// Keep an old active release even though it is outside the newest
	// inactive window. This is the rollback safety invariant.
	if err := os.Symlink(filepath.Join("releases", ids[0]), filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}

	report, err := Prune(releases, filepath.Join(root, "current"), 3)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if report.Current != ids[0] {
		t.Fatalf("Current = %q, want %q", report.Current, ids[0])
	}
	if len(report.Removed) != 1 || report.Removed[0] != ids[1] {
		t.Fatalf("Removed = %v, want [%s]", report.Removed, ids[1])
	}
	for _, id := range ids[0:1] {
		if _, err := os.Stat(filepath.Join(releases, id)); err != nil {
			t.Fatalf("kept current %s: %v", id, err)
		}
	}
	for _, id := range ids[2:] {
		if _, err := os.Stat(filepath.Join(releases, id)); err != nil {
			t.Fatalf("kept recent %s: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(releases, ids[1])); !os.IsNotExist(err) {
		t.Fatalf("old release %s still exists, stat err=%v", ids[1], err)
	}
}

func TestPruneLeavesNonReleaseEntriesUntouched(t *testing.T) {
	root := t.TempDir()
	releases := filepath.Join(root, "releases")
	if err := os.MkdirAll(filepath.Join(releases, "legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releases, "staging.tmp"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("legacy", filepath.Join(releases, "linked")); err != nil {
		t.Fatal(err)
	}
	report, err := Prune(releases, filepath.Join(root, "current"), 1)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if report.Current != "" || len(report.Removed) != 0 {
		t.Fatalf("report = %+v, want no release changes", report)
	}
	for _, path := range []string{"legacy", "staging.tmp", "linked"} {
		if _, err := os.Lstat(filepath.Join(releases, path)); err != nil {
			t.Fatalf("entry %s changed: %v", path, err)
		}
	}
}

func TestPruneRejectsUnsafeCurrent(t *testing.T) {
	root := t.TempDir()
	releases := filepath.Join(root, "releases")
	if err := os.MkdirAll(releases, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp/outside", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if _, err := Prune(releases, filepath.Join(root, "current"), 1); err == nil {
		t.Fatal("Prune returned nil for an unsafe current target")
	}
}

// TestPruneKeepsTheBaseOfAnActiveUnitRelease: a per-unit release names its
// base full release in release-manifest.json. Retention must keep that base
// however many unit releases followed it.
func TestPruneKeepsTheBaseOfAnActiveUnitRelease(t *testing.T) {
	root := t.TempDir()
	releases := filepath.Join(root, "releases")
	base := "00000000000000000000000000000000000000b0"
	ids := []string{
		base,
		"0000000000000000000000000000000000000001",
		"0000000000000000000000000000000000000002",
		"0000000000000000000000000000000000000003",
		"0000000000000000000000000000000000000004",
	}
	for i, id := range ids {
		path := filepath.Join(releases, id)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		// Every unit release carries the base's canonical manifest.
		if err := os.WriteFile(filepath.Join(path, "release-manifest.json"), []byte(`{"git_sha":"`+base+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Unix(int64(i+1), 0)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	current := filepath.Join(root, "current")
	if err := os.Symlink(filepath.Join(releases, ids[4]), current); err != nil {
		t.Fatal(err)
	}
	report, err := Prune(releases, current, 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(report.Removed) != 1 || report.Removed[0] != ids[1] {
		t.Fatalf("Removed = %v, want only %s", report.Removed, ids[1])
	}
	if _, err := os.Stat(filepath.Join(releases, base)); err != nil {
		t.Fatalf("base of the active unit release was pruned: %v", err)
	}

	// Once a full release is active again, the old base is an ordinary
	// previous release.
	full := "00000000000000000000000000000000000000f0"
	if err := os.MkdirAll(filepath.Join(releases, full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releases, full, "release-manifest.json"), []byte(`{"git_sha":"`+full+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(releases, full), current); err != nil {
		t.Fatal(err)
	}
	if _, err := Prune(releases, current, 2); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(releases, base)); !os.IsNotExist(err) {
		t.Fatalf("old base survived after a full release took over: %v", err)
	}
}

// TestPruneKeepsProtectedReleases: the deploy controller protects the release
// a rollback of the active one restores. Newer releases that were rolled back
// can push it out of the window by directory time.
func TestPruneKeepsProtectedReleases(t *testing.T) {
	root := t.TempDir()
	releases := filepath.Join(root, "releases")
	ids := []string{
		"0000000000000000000000000000000000000001", // rollback target
		"0000000000000000000000000000000000000002",
		"0000000000000000000000000000000000000003",
		"0000000000000000000000000000000000000004", // active
	}
	for i, id := range ids {
		path := filepath.Join(releases, id)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		when := time.Unix(int64(i+1), 0)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join("releases", ids[3]), filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	report, err := Prune(releases, filepath.Join(root, "current"), 1, ids[0])
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(report.Removed) != 1 || report.Removed[0] != ids[1] {
		t.Fatalf("Removed = %v, want only %s", report.Removed, ids[1])
	}
	if _, err := os.Stat(filepath.Join(releases, ids[0])); err != nil {
		t.Fatalf("protected release pruned: %v", err)
	}
}
