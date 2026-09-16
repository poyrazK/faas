package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

func TestAbandonedBundleCandidatesRequiresMissingOldSupersededBundle(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	oldMissing := releaseinstall.BundleRow{GitSHA: "0000000000000000000000000000000000000001", CreatedAt: now.Add(-48 * time.Hour)}
	oldPresent := releaseinstall.BundleRow{GitSHA: "0000000000000000000000000000000000000002", CreatedAt: now.Add(-47 * time.Hour)}
	currentMissing := releaseinstall.BundleRow{GitSHA: "0000000000000000000000000000000000000003", CreatedAt: now.Add(-time.Hour)}
	appliedAt := now.Add(-24 * time.Hour)
	newerApplied := releaseinstall.BundleRow{GitSHA: "0000000000000000000000000000000000000004", CreatedAt: now.Add(-25 * time.Hour), AppliedAt: &appliedAt}
	if err := os.MkdirAll(filepath.Join(root, oldPresent.GitSHA), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := abandonedBundleCandidates([]releaseinstall.BundleRow{currentMissing, newerApplied, oldPresent, oldMissing}, root, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].GitSHA != oldMissing.GitSHA {
		t.Fatalf("eligible = %+v, want only old missing superseded bundle", got)
	}
}

func TestCmdReleaseReconcileHelpAndValidation(t *testing.T) {
	if code := cmdReleaseReconcile([]string{"--help"}); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	if code := cmdReleaseReconcile([]string{"--retention=0"}); code != 1 {
		t.Fatalf("zero retention exit = %d, want 1", code)
	}
}
