package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

// standInCosign accepts the fake cosign at path as the pinned build for one
// test, so fixtures need not ship the real 100 MB binary.
func standInCosign(t *testing.T, path string) {
	t.Helper()
	sum, err := prepareFileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	prev := pinnedCosignSHA256
	pinnedCosignSHA256 = sum
	t.Cleanup(func() { pinnedCosignSHA256 = prev })
}

func TestCosignPinDefaultsToTheReleaseBuild(t *testing.T) {
	if pinnedCosignSHA256 != releaseinstall.PinnedCosignSHA256 {
		t.Fatalf("pinnedCosignSHA256 = %s, want releaseinstall.PinnedCosignSHA256", pinnedCosignSHA256)
	}
}

func TestDeployPrepareRefusesAnUnpinnedCosignBeforeDownloading(t *testing.T) {
	cosign := filepath.Join(t.TempDir(), "cosign")
	if err := os.WriteFile(cosign, []byte("some other cosign"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := releaseinstall.CheckCosignBinary(cosign, pinnedCosignSHA256)
	if err == nil || !strings.Contains(err.Error(), releaseinstall.PinnedCosignURL) {
		t.Fatalf("CheckCosignBinary = %v, want a refusal naming %s", err, releaseinstall.PinnedCosignURL)
	}
	// The real prepare flow runs the same check right after resolving the
	// binary, before any release download.
	body, err := os.ReadFile("commands_deploy_prepare.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	resolve := strings.Index(src, "cosignPath, err := resolvePrepareCosignBinary(opts.CosignBinary)")
	check := strings.Index(src, "releaseinstall.CheckCosignBinary(cosignPath, pinnedCosignSHA256)")
	download := strings.Index(src, "prepareReleaseAssets(ctx, prepareReleaseOptions{")
	if resolve < 0 || check < resolve || download < check {
		t.Fatalf("deploy prepare must check the cosign pin after resolving it and before downloading the release (resolve=%d check=%d download=%d)", resolve, check, download)
	}
}
