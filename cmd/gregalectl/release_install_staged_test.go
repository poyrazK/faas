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

// newReleasesRoot returns <tmp>/releases; its staging area is <tmp>/staged-releases.
func newReleasesRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "releases")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func runInstall(t *testing.T, root, tarballPath string, stageOnly bool, identity string) (int, string, string, *releaseinstall.FixtureCosignVerifier) {
	t.Helper()
	var out, errOut bytes.Buffer
	verifier := &releaseinstall.FixtureCosignVerifier{Identity: identity}
	code := installOrReuseStaged(root, stagedTestSHA, tarballPath, stageOnly, verifier, &out, &errOut)
	return code, out.String(), errOut.String(), verifier
}

func stageForTest(t *testing.T, root, tarballPath string) {
	t.Helper()
	code, out, errOut, _ := runInstall(t, root, tarballPath, true, "ci-identity")
	if code != 0 {
		t.Fatalf("stage-only exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, `"staged": true`) {
		t.Fatalf("stage-only report = %s", out)
	}
}

func assertVerifies(t *testing.T, root string) {
	t.Helper()
	m, err := releaseinstall.Read(root, stagedTestSHA)
	if err != nil {
		t.Fatal(err)
	}
	if err := releaseinstall.Verify(root, m); err != nil {
		t.Fatalf("tree under %s does not verify: %v", root, err)
	}
}

func assertAbsent(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s still exists (%s): %v", what, path, err)
	}
}

func TestStageOnlyStagesBesideTheReleasesRoot(t *testing.T) {
	root := newReleasesRoot(t)
	stageForTest(t, root, writeStagedTestTriple(t, "v1"))

	staging := stagingRoot(root)
	if _, err := os.Stat(stagedInstallMarkerPath(staging, stagedTestSHA)); err != nil {
		t.Fatalf("marker: %v", err)
	}
	assertVerifies(t, staging)
	// The releases root, and so retention, rollback selection and bundle
	// verification, never see a prepared-but-not-activated release.
	assertAbsent(t, releaseinstall.BundleRoot(root, stagedTestSHA), "a release directory")
	if sha, err := releaseinstall.CurrentGitSHA(root); err != nil || sha != "" {
		t.Fatalf("stage-only flipped current: sha=%q err=%v", sha, err)
	}
}

func TestActivationMovesTheStagedTreeIntoPlace(t *testing.T) {
	root := newReleasesRoot(t)
	tarballPath := writeStagedTestTriple(t, "v1")
	stageForTest(t, root, tarballPath)
	staged := filepath.Join(releaseinstall.BinDir(stagingRoot(root), stagedTestSHA), manifest.SortedHostKeys()[0])
	before, err := os.Stat(staged)
	if err != nil {
		t.Fatal(err)
	}

	code, _, errOut, verifier := runInstall(t, root, tarballPath, false, "ci-identity")
	if code != -1 {
		t.Fatalf("exit %d, want -1 (continue to the flip): %s", code, errOut)
	}
	if !strings.Contains(errOut, "reusing the release verified and extracted during preparation") {
		t.Fatalf("stderr = %q, want the reuse line", errOut)
	}
	if verifier.CallCount != 1 {
		t.Fatalf("cosign calls = %d, want 1 at activation", verifier.CallCount)
	}
	installed := filepath.Join(releaseinstall.BinDir(root, stagedTestSHA), manifest.SortedHostKeys()[0])
	after, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("activation re-extracted instead of moving the staged tree")
	}
	assertVerifies(t, root)
	assertAbsent(t, stagedInstallMarkerPath(root, stagedTestSHA), "the marker in the installed release")
	assertAbsent(t, releaseinstall.BundleRoot(stagingRoot(root), stagedTestSHA), "the staged tree")
}

func TestStagedTreeIsRefusedAndReinstalled(t *testing.T) {
	staged := func(root string) string { return releaseinstall.BundleRoot(stagingRoot(root), stagedTestSHA) }
	cases := map[string]func(t *testing.T, root, tarballPath string) (string, string){
		"different release bytes": func(t *testing.T, _, _ string) (string, string) {
			return writeStagedTestTriple(t, "v2"), "ci-identity"
		},
		"retained tarball changed": func(t *testing.T, root, tarballPath string) (string, string) {
			if err := os.WriteFile(filepath.Join(staged(root), releaseTarballName), []byte("swapped"), 0o644); err != nil {
				t.Fatal(err)
			}
			return tarballPath, "ci-identity"
		},
		"manifest edited": func(t *testing.T, root, tarballPath string) (string, string) {
			m, err := releaseinstall.Read(stagingRoot(root), stagedTestSHA)
			if err != nil {
				t.Fatal(err)
			}
			m.ManifestHash = "sha256:" + strings.Repeat("b", 64)
			if err := releaseinstall.Write(stagingRoot(root), m); err != nil {
				t.Fatal(err)
			}
			return tarballPath, "ci-identity"
		},
		// Review round 3: a damaged binary must fall back to the full
		// install, which repairs the tree, instead of failing every retry.
		"staged binary damaged": func(t *testing.T, root, tarballPath string) (string, string) {
			bin := filepath.Join(releaseinstall.BinDir(stagingRoot(root), stagedTestSHA), manifest.SortedHostKeys()[0])
			if err := os.WriteFile(bin, []byte("truncated"), 0o755); err != nil {
				t.Fatal(err)
			}
			return tarballPath, "ci-identity"
		},
		"other signer": func(_ *testing.T, _, tarballPath string) (string, string) {
			return tarballPath, "someone-else"
		},
		"stage never finished": func(t *testing.T, root, tarballPath string) (string, string) {
			if err := os.Remove(stagedInstallMarkerPath(stagingRoot(root), stagedTestSHA)); err != nil {
				t.Fatal(err)
			}
			return tarballPath, "ci-identity"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			root := newReleasesRoot(t)
			stagedFrom := writeStagedTestTriple(t, "v1")
			stageForTest(t, root, stagedFrom)
			tarballPath, identity := mutate(t, root, stagedFrom)

			code, _, errOut, _ := runInstall(t, root, tarballPath, false, identity)
			if strings.Contains(errOut, "reusing the release") {
				t.Fatalf("reused a staged tree after %s: %s", name, errOut)
			}
			if !strings.Contains(errOut, "not reusing the staged release") {
				t.Fatalf("stderr = %q, want the refusal reason", errOut)
			}
			assertAbsent(t, staged(root), "the staged tree")
			if name == "other signer" {
				return // the full install stamps whatever cosign reports
			}
			if code != -1 {
				t.Fatalf("full install exit %d: %s", code, errOut)
			}
			assertVerifies(t, root)
		})
	}
}

// Review round 2: a stage never works on a release that is installed.
func TestStageOnlyLeavesInstalledReleasesAlone(t *testing.T) {
	t.Run("active release", func(t *testing.T) {
		root := newReleasesRoot(t)
		tarballPath := writeStagedTestTriple(t, "v1")
		if code, _, errOut, _ := runInstall(t, root, tarballPath, false, "ci-identity"); code != -1 {
			t.Fatalf("install exit %d: %s", code, errOut)
		}
		if err := releaseinstall.AtomicFlip(root, stagedTestSHA); err != nil {
			t.Fatal(err)
		}
		assertStageSkipped(t, root, tarballPath, "active release")
	})
	t.Run("already installed", func(t *testing.T) {
		root := newReleasesRoot(t)
		if err := os.MkdirAll(releaseinstall.BundleRoot(root, stagedTestSHA), 0o755); err != nil {
			t.Fatal(err)
		}
		assertStageSkipped(t, root, writeStagedTestTriple(t, "v1"), "already installed")
	})
}

func assertStageSkipped(t *testing.T, root, tarballPath, want string) {
	t.Helper()
	code, out, errOut, verifier := runInstall(t, root, tarballPath, true, "ci-identity")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, `"staged": false`) || !strings.Contains(out, want) {
		t.Fatalf("report = %s, want a skip naming %q", out, want)
	}
	if verifier.CallCount != 0 {
		t.Fatalf("cosign ran %d times for a skipped stage", verifier.CallCount)
	}
	assertAbsent(t, releaseinstall.BundleRoot(stagingRoot(root), stagedTestSHA), "a staged tree")
}

// Review round 3: the staging area holds at most the stage being prepared;
// aborted rollouts' stages and interrupted stages do not pile up.
func TestStageOnlyClearsStaleAndPartialStages(t *testing.T) {
	root := newReleasesRoot(t)
	staging := stagingRoot(root)
	other := filepath.Join(staging, strings.Repeat("f", 40))
	if err := os.MkdirAll(filepath.Join(other, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	// An interrupted stage of this release: a tree without a marker.
	partial := releaseinstall.BinDir(staging, stagedTestSHA)
	if err := os.MkdirAll(partial, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partial, "half-written"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	stageForTest(t, root, writeStagedTestTriple(t, "v1"))

	assertAbsent(t, other, "an aborted rollout's stage")
	assertAbsent(t, filepath.Join(partial, "half-written"), "the interrupted stage's debris")
	assertVerifies(t, staging)
}

func TestStageOnlyRequiresTarballPath(t *testing.T) {
	if code := cmdReleaseInstall([]string{"--git-sha", stagedTestSHA, "--stage-only"}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}
