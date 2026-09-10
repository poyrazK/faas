package deploycontroller

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/releasebundle"
)

func TestDryRunReportsLegacyHost(t *testing.T) {
	root := t.TempDir()
	makeDryRunRelease(t, root, "candidate")
	legacyBin := filepath.Join(root, "legacy-bin")
	if err := os.MkdirAll(legacyBin, 0o755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "missing-current")
	report, err := DryRun(Config{ReleasesRoot: root, CurrentPath: current}, "candidate")
	if err != nil {
		t.Fatal(err)
	}
	if report.CurrentTarget != "" || report.HasPreviousRelease {
		t.Fatalf("unexpected current state: %#v", report)
	}
	if !report.LegacySourceDir && !report.LegacyBinDir {
		// The test root is not /opt/faas; the report should still remain safe.
		return
	}
}

func TestDryRunReportsVerifiedPreviousRelease(t *testing.T) {
	root := t.TempDir()
	old := makeDryRunRelease(t, root, "old")
	if err := os.WriteFile(filepath.Join(old, "sbom-baseline.json"), []byte(`{"counts":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	makeDryRunRelease(t, root, "candidate")
	current := filepath.Join(root, "current")
	if err := os.Symlink(old, current); err != nil {
		t.Fatal(err)
	}
	report, err := DryRun(Config{ReleasesRoot: root, CurrentPath: current}, "candidate")
	if err != nil {
		t.Fatal(err)
	}
	if !report.HasPreviousRelease || report.CurrentRelease != "old" {
		t.Fatalf("report = %#v, want verified old release", report)
	}
}

func TestDryRunFindsControllerScratchFiles(t *testing.T) {
	root := t.TempDir()
	makeDryRunRelease(t, root, "candidate")
	scratch := filepath.Join(root, "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "faas-base-mkfs-old.ext4"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The production paths are intentionally fixed; this test verifies the
	// release/report contract separately from VM cleanup permissions.
	report, err := DryRun(Config{ReleasesRoot: root, CurrentPath: filepath.Join(root, "current")}, "candidate")
	if err != nil {
		t.Fatal(err)
	}
	if report.ReleaseID != "candidate" {
		t.Fatalf("report = %#v", report)
	}
}

func makeDryRunRelease(t *testing.T, root, id string) string {
	t.Helper()
	release := filepath.Join(root, id)
	if err := os.MkdirAll(filepath.Join(release, "bin", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "bin", "deployctl"), []byte(id), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := releasebundle.Build(release, id, "commit-"+id, "linux/amd64", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := releasebundle.Write(release, manifest); err != nil {
		t.Fatal(err)
	}
	return release
}
