package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

const stagedTestSHA = "0123456789abcdef0123456789abcdef01234567"

// writeStagedTestTriple builds a canonical release triple whose binaries carry
// variant, so two calls with different variants give two different tarballs.
func writeStagedTestTriple(t *testing.T, variant string) string {
	t.Helper()
	buildRoot := t.TempDir()
	binDir := releaseinstall.BinDir(buildRoot, stagedTestSHA)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range manifest.SortedHostKeys() {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(variant+"-"+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tb, err := releaseinstall.BuildTarball(buildRoot, stagedTestSHA, "sha256:"+strings.Repeat("a", 64), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("BuildTarball: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, releaseTarballName)
	for name, body := range map[string][]byte{
		releaseTarballName: tb.Packed,
		releaseSigName:     []byte("signature"),
		releaseSBOMName:    []byte(`{"spdxVersion":"SPDX-2.3"}`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func stageForTest(t *testing.T, root, tarballPath string) {
	t.Helper()
	var out, errOut bytes.Buffer
	verifier := &releaseinstall.FixtureCosignVerifier{Identity: "ci-identity"}
	if code := installOrReuseStaged(root, stagedTestSHA, tarballPath, true, verifier, &out, &errOut); code != 0 {
		t.Fatalf("stage-only exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"staged": true`) || !strings.Contains(out.String(), `"reused": false`) {
		t.Fatalf("stage-only report = %s", out.String())
	}
}

func TestStageOnlyExtractsAndRecordsWithoutActivating(t *testing.T) {
	root := t.TempDir()
	stageForTest(t, root, writeStagedTestTriple(t, "v1"))

	if _, err := os.Stat(stagedInstallMarkerPath(root, stagedTestSHA)); err != nil {
		t.Fatalf("marker: %v", err)
	}
	if sha, err := releaseinstall.CurrentGitSHA(root); err != nil || sha != "" {
		t.Fatalf("stage-only flipped current: sha=%q err=%v", sha, err)
	}
	m, err := releaseinstall.Read(root, stagedTestSHA)
	if err != nil {
		t.Fatal(err)
	}
	if err := releaseinstall.Verify(root, m); err != nil {
		t.Fatalf("staged tree does not verify: %v", err)
	}
}

func TestActivationReusesTheStagedTree(t *testing.T) {
	root := t.TempDir()
	tarballPath := writeStagedTestTriple(t, "v1")
	stageForTest(t, root, tarballPath)

	probe := filepath.Join(releaseinstall.BinDir(root, stagedTestSHA), manifest.SortedHostKeys()[0])
	before, err := os.Stat(probe)
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	verifier := &releaseinstall.FixtureCosignVerifier{Identity: "ci-identity"}
	if code := installOrReuseStaged(root, stagedTestSHA, tarballPath, false, verifier, &out, &errOut); code != -1 {
		t.Fatalf("exit %d, want -1 (continue to the flip): %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "reusing the release verified and extracted during preparation") {
		t.Fatalf("stderr = %q, want the reuse line", errOut.String())
	}
	// Cosign still runs at activation, on the retained bytes.
	if verifier.CallCount != 1 || verifier.CallTarball != filepath.Join(releaseinstall.BundleRoot(root, stagedTestSHA), releaseTarballName) {
		t.Fatalf("cosign calls = %d on %q", verifier.CallCount, verifier.CallTarball)
	}
	after, err := os.Stat(probe)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("activation re-extracted a binary the stage already wrote")
	}
}

func TestStagedTreeIsRefusedAndReinstalled(t *testing.T) {
	cases := map[string]func(t *testing.T, root, tarballPath string) (string, releaseinstall.CosignVerifier){
		"different release bytes": func(t *testing.T, _, _ string) (string, releaseinstall.CosignVerifier) {
			return writeStagedTestTriple(t, "v2"), &releaseinstall.FixtureCosignVerifier{Identity: "ci-identity"}
		},
		"retained tarball changed": func(t *testing.T, root, tarballPath string) (string, releaseinstall.CosignVerifier) {
			retained := filepath.Join(releaseinstall.BundleRoot(root, stagedTestSHA), releaseTarballName)
			if err := os.WriteFile(retained, []byte("swapped"), 0o644); err != nil {
				t.Fatal(err)
			}
			return tarballPath, &releaseinstall.FixtureCosignVerifier{Identity: "ci-identity"}
		},
		"manifest edited": func(t *testing.T, root, tarballPath string) (string, releaseinstall.CosignVerifier) {
			m, err := releaseinstall.Read(root, stagedTestSHA)
			if err != nil {
				t.Fatal(err)
			}
			m.ManifestHash = "sha256:" + strings.Repeat("b", 64)
			if err := releaseinstall.Write(root, m); err != nil {
				t.Fatal(err)
			}
			return tarballPath, &releaseinstall.FixtureCosignVerifier{Identity: "ci-identity"}
		},
		"other signer": func(_ *testing.T, _, tarballPath string) (string, releaseinstall.CosignVerifier) {
			return tarballPath, &releaseinstall.FixtureCosignVerifier{Identity: "someone-else"}
		},
		"marker for another release": func(t *testing.T, root, tarballPath string) (string, releaseinstall.CosignVerifier) {
			path := stagedInstallMarkerPath(root, stagedTestSHA)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.Replace(body, []byte(stagedTestSHA), []byte(strings.Repeat("f", 40)), 1)
			if err := os.WriteFile(path, body, 0o644); err != nil {
				t.Fatal(err)
			}
			return tarballPath, &releaseinstall.FixtureCosignVerifier{Identity: "ci-identity"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			staged := writeStagedTestTriple(t, "v1")
			stageForTest(t, root, staged)
			tarballPath, verifier := mutate(t, root, staged)

			var out, errOut bytes.Buffer
			code := installOrReuseStaged(root, stagedTestSHA, tarballPath, false, verifier, &out, &errOut)
			if strings.Contains(errOut.String(), "reusing the release") {
				t.Fatalf("reused a staged tree after %s: %s", name, errOut.String())
			}
			if !strings.Contains(errOut.String(), "not reusing the staged release") {
				t.Fatalf("stderr = %q, want the refusal reason", errOut.String())
			}
			if name == "other signer" {
				// The full install stamps whatever identity cosign reports;
				// the refusal is what matters here.
				return
			}
			if code != -1 {
				t.Fatalf("full install exit %d: %s", code, errOut.String())
			}
			if _, err := os.Stat(stagedInstallMarkerPath(root, stagedTestSHA)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("full install left the stage marker: %v", err)
			}
			m, err := releaseinstall.Read(root, stagedTestSHA)
			if err != nil {
				t.Fatal(err)
			}
			if err := releaseinstall.Verify(root, m); err != nil {
				t.Fatalf("reinstalled tree does not verify: %v", err)
			}
		})
	}
}

// A binary swapped after preparation passes the marker checks by design: the
// on-disk Verify that cmdReleaseInstall runs before the flip catches it.
func TestTamperedStagedBinaryFailsTheOnDiskVerify(t *testing.T) {
	root := t.TempDir()
	tarballPath := writeStagedTestTriple(t, "v1")
	stageForTest(t, root, tarballPath)
	bin := filepath.Join(releaseinstall.BinDir(root, stagedTestSHA), manifest.SortedHostKeys()[0])
	if err := os.WriteFile(bin, []byte("swapped"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	verifier := &releaseinstall.FixtureCosignVerifier{Identity: "ci-identity"}
	if code := installOrReuseStaged(root, stagedTestSHA, tarballPath, false, verifier, &out, &errOut); code != -1 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	m, err := releaseinstall.Read(root, stagedTestSHA)
	if err != nil {
		t.Fatal(err)
	}
	if err := releaseinstall.Verify(root, m); err == nil {
		t.Fatal("on-disk Verify accepted a swapped staged binary")
	}
}

// Review round 2: a stage marks only directories it creates, so retention can
// never mistake the active release or a rollback target for an abandoned stage.
func TestStageOnlyLeavesInstalledReleasesUnmarked(t *testing.T) {
	t.Run("active release", func(t *testing.T) {
		root := t.TempDir()
		tarballPath := writeStagedTestTriple(t, "v1")
		stageForTest(t, root, tarballPath)
		if err := releaseinstall.AtomicFlip(root, stagedTestSHA); err != nil {
			t.Fatal(err)
		}
		if err := removeStagedInstallMarker(root, stagedTestSHA); err != nil { // as activation does
			t.Fatal(err)
		}
		assertStageSkipped(t, root, tarballPath, "active release")
	})
	t.Run("earlier install without a marker", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(releaseinstall.BundleRoot(root, stagedTestSHA), 0o755); err != nil {
			t.Fatal(err)
		}
		assertStageSkipped(t, root, writeStagedTestTriple(t, "v1"), "earlier install")
	})
}

func assertStageSkipped(t *testing.T, root, tarballPath, want string) {
	t.Helper()
	var out, errOut bytes.Buffer
	verifier := &releaseinstall.FixtureCosignVerifier{Identity: "ci-identity"}
	if code := installOrReuseStaged(root, stagedTestSHA, tarballPath, true, verifier, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"staged": false`) || !strings.Contains(out.String(), want) {
		t.Fatalf("report = %s, want a skip naming %q", out.String(), want)
	}
	if verifier.CallCount != 0 {
		t.Fatalf("cosign ran %d times for a skipped stage", verifier.CallCount)
	}
	if _, err := os.Stat(stagedInstallMarkerPath(root, stagedTestSHA)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a skipped stage left a marker: %v", err)
	}
}

func TestStageOnlyRequiresTarballPath(t *testing.T) {
	if code := cmdReleaseInstall([]string{"--git-sha", stagedTestSHA, "--stage-only"}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}
