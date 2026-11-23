package releaseretention

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

func TestStagedInstallMarkerNameMatchesReleaseinstall(t *testing.T) {
	if stagedInstallMarker != releaseinstall.StagedInstallMarkerName {
		t.Fatalf("stagedInstallMarker = %q, releaseinstall says %q", stagedInstallMarker, releaseinstall.StagedInstallMarkerName)
	}
}

// ADR-20261123150000: a release preparation staged but no activation switched
// to must not push the real rollback target out of the keep-previous window.
// Without an active release nothing is "newer than current": staged
// directories count like any other, so they cannot pile up forever.
func TestPruneWithoutCurrentTreatsStagesLikeReleases(t *testing.T) {
	root := t.TempDir()
	releases := filepath.Join(root, "releases")
	ids := []string{
		"0000000000000000000000000000000000000001",
		"0000000000000000000000000000000000000002",
		"0000000000000000000000000000000000000003",
	}
	for i, id := range ids {
		dir := filepath.Join(releases, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, stagedInstallMarker), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Unix(int64(i+1)*100, 0)
		if err := os.Chtimes(dir, when, when); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Prune(releases, filepath.Join(root, "current"), 1)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(report.Removed) != 2 {
		t.Fatalf("Removed = %v, want the two older stages", report.Removed)
	}
}

func TestPruneDoesNotLetAnUnactivatedStageTakeARollbackSlot(t *testing.T) {
	root := t.TempDir()
	releases := filepath.Join(root, "releases")
	const (
		rollback  = "0000000000000000000000000000000000000001" // previous active release
		abandoned = "0000000000000000000000000000000000000002" // prepared, never activated
		current   = "0000000000000000000000000000000000000003"
		upcoming  = "0000000000000000000000000000000000000004" // prepared after current
	)
	for i, id := range []string{rollback, abandoned, current, upcoming} {
		dir := filepath.Join(releases, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if id == abandoned || id == upcoming {
			if err := os.WriteFile(filepath.Join(dir, stagedInstallMarker), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		when := time.Unix(int64(i+1)*100, 0)
		if err := os.Chtimes(dir, when, when); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join("releases", current), filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}

	report, err := Prune(releases, filepath.Join(root, "current"), 1)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if !slices.Equal(report.Removed, []string{abandoned}) {
		t.Fatalf("Removed = %v, want only the abandoned stage %s", report.Removed, abandoned)
	}
	for _, id := range []string{rollback, current, upcoming} {
		if _, err := os.Stat(filepath.Join(releases, id)); err != nil {
			t.Fatalf("%s was pruned: %v", id, err)
		}
	}
}
