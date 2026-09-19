package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/releasebundle"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

func TestRunBundleCheckAcceptsInstalledSBOMBaselineOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "daemon"), []byte("release"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := releasebundle.Build(root, "release", "0123456789abcdef0123456789abcdef01234567", "linux/amd64", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := releasebundle.Write(root, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, releaseinstall.SBOMBaselineName), []byte(`{"counts":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runBundleCheck([]string{root}); err == nil {
		t.Fatal("strict candidate check accepted the host-owned KGV baseline")
	}
	if err := runInstalledBundleCheck([]string{root}); err != nil {
		t.Fatalf("installed release check rejected KGV baseline: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("drift"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInstalledBundleCheck([]string{root}); err == nil {
		t.Fatal("installed release check accepted an unrelated extra file")
	}
}
