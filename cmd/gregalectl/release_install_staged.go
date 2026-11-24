package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

// ADR-20261123150000: rollout preparation runs `release install
// --stage-only`, which does every expensive step of the canonical tarball
// install (cosign, the per-member hash walk, extraction, retained assets)
// while the node still serves — into a staging area BESIDE the releases root
// (/opt/faas/staged-releases/<sha>), never inside it. Activation's ordinary
// `release install --tarball-path` re-checks the staged tree and moves it
// into <releases-root>/<sha> with one rename, instead of decompressing the
// 489 MB bundle three more times on the drained node's critical path (32 s per
// node on rc.371–rc.374).
//
// Keeping stages out of the releases root means retention, deploycontroller
// rollback selection and bundle verification never see a release that was
// prepared but not activated, and an interrupted stage can only leave debris
// in the staging area, which the next stage clears.
//
// Reuse never widens trust: activation re-runs cosign on the exact bytes it
// was handed, requires them to be the bytes preparation verified, hashes every
// staged binary against the manifest before the move, and the caller's
// on-disk Verify still runs before the flip. Anything else falls back to the
// full install from the tarball.

const stagedInstallMarkerName = "staged-install.json"

// releaseInstallLockName is the lock file in the staging area; stage cleanup
// keeps it.
const releaseInstallLockName = ".install.lock"

// stagedInstallMarkerFormat is bumped when the marker's meaning changes; an
// unknown format is never reused.
const stagedInstallMarkerFormat = 2

type stagedInstallMarker struct {
	Format int    `json:"format"`
	GitSHA string `json:"git_sha"`
	// Digests of the canonical triple the stage verified and retained.
	TarballSHA256   string `json:"tarball_sha256"`
	SignatureSHA256 string `json:"signature_sha256"`
	SBOMSHA256      string `json:"sbom_sha256"`
	// SignedManifestDigest ties the extracted manifest to the signed tarball
	// member that hashWalk checked (releaseinstall.SignedManifestDigest).
	SignedManifestDigest string `json:"signed_manifest_digest"`
	// Identity is the cosign certificate identity stamped on the manifest.
	Identity string `json:"identity"`
}

// stagingRoot is the staging area for a releases root: a sibling directory on
// the same filesystem, so activation can rename a staged tree into place.
func stagingRoot(releasesRoot string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(releasesRoot)), "staged-releases")
}

func stagedInstallMarkerPath(root, gitSHA string) string {
	return filepath.Join(releaseinstall.BundleRoot(root, gitSHA), stagedInstallMarkerName)
}

// removeStagedInstallMarker drops a marker before any full extraction into
// root, so a tree interrupted mid-extract is never mistaken for a finished
// stage. Outside the staging area there is never a marker; this is a no-op.
func removeStagedInstallMarker(root, gitSHA string) error {
	err := os.Remove(stagedInstallMarkerPath(root, gitSHA))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove staged-install marker: %w", err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:forbidigo // operator-supplied release triple or the root-owned staged copy of it.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func bytesSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// canonicalTripleDigests hashes the tarball the caller supplied and its
// sibling cosign bundle and SBoM (same lookup as installViaTarballWithVerifier).
func canonicalTripleDigests(tarballPath string) (tarball, sig, sbom string, err error) {
	if tarball, err = fileSHA256(tarballPath); err != nil {
		return "", "", "", fmt.Errorf("hash tarball: %w", err)
	}
	sigBody, err := readCanonicalReleaseAsset(tarballPath, releaseSigName)
	if err != nil {
		return "", "", "", fmt.Errorf("read cosign bundle: %w", err)
	}
	sbomBody, err := readCanonicalReleaseAsset(tarballPath, releaseSBOMName)
	if err != nil {
		return "", "", "", fmt.Errorf("read sbom: %w", err)
	}
	return tarball, bytesSHA256(sigBody), bytesSHA256(sbomBody), nil
}

// retainedTripleDigests hashes the copies a stage retained in its tree: the
// exact bytes installViaTarballWithVerifier verified and extracted from.
func retainedTripleDigests(root, gitSHA string) (map[string]string, error) {
	bundle := releaseinstall.BundleRoot(root, gitSHA)
	out := make(map[string]string, 3)
	for _, name := range []string{releaseTarballName, releaseSigName, releaseSBOMName} {
		sum, err := fileSHA256(filepath.Join(bundle, name))
		if err != nil {
			return nil, fmt.Errorf("hash retained %s: %w", name, err)
		}
		out[name] = sum
	}
	return out, nil
}

// writeStagedInstallMarker records a finished stage. It is called only after
// the extraction and the on-disk Verify succeeded, and it is the stage's last
// write.
func writeStagedInstallMarker(root, gitSHA string, m releaseinstall.Manifest) error {
	retained, err := retainedTripleDigests(root, gitSHA)
	if err != nil {
		return fmt.Errorf("staged-install marker: %w", err)
	}
	digest, err := releaseinstall.SignedManifestDigest(m)
	if err != nil {
		return fmt.Errorf("staged-install marker: signed manifest digest: %w", err)
	}
	body, err := json.MarshalIndent(stagedInstallMarker{
		Format:               stagedInstallMarkerFormat,
		GitSHA:               gitSHA,
		TarballSHA256:        retained[releaseTarballName],
		SignatureSHA256:      retained[releaseSigName],
		SBOMSHA256:           retained[releaseSBOMName],
		SignedManifestDigest: digest,
		Identity:             m.Signature,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("staged-install marker: encode: %w", err)
	}
	dir := releaseinstall.BundleRoot(root, gitSHA)
	tmp, err := os.CreateTemp(dir, "."+stagedInstallMarkerName+"-*.tmp")
	if err != nil {
		return fmt.Errorf("staged-install marker: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("staged-install marker: chmod: %w", err)
	}
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("staged-install marker: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("staged-install marker: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("staged-install marker: close: %w", err)
	}
	if err := os.Rename(tmpPath, stagedInstallMarkerPath(root, gitSHA)); err != nil {
		return fmt.Errorf("staged-install marker: publish: %w", err)
	}
	return nil
}

// installOrReuseStaged runs the tarball half of `release install`. It returns
// -1 when the caller should continue to the on-disk Verify, SBoM gate and
// flip, or the exit code to return now (an error, or a finished
// --stage-only).
func installOrReuseStaged(releasesRoot, gitSHA, tarballPath string, stageOnly bool, verifier releaseinstall.CosignVerifier, stdout, stderr io.Writer) int {
	staging := stagingRoot(releasesRoot)
	// The lock guards the staging area: a stage left running by a failed
	// prepare play and a rerun or activation never work on it at once.
	unlock, err := lockReleaseInstalls(staging)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl release install: %v\n", err)
		return 3
	}
	defer unlock()
	if stageOnly {
		return stageRelease(releasesRoot, staging, gitSHA, tarballPath, verifier, stdout, stderr)
	}

	stagedDir := releaseinstall.BundleRoot(staging, gitSHA)
	target := releaseinstall.BundleRoot(releasesRoot, gitSHA)
	reused := false
	if _, err := os.Lstat(stagedDir); err == nil {
		why := ""
		if _, targetErr := os.Lstat(target); targetErr == nil {
			why = "the release is already on this host"
		} else if !errors.Is(targetErr, os.ErrNotExist) {
			why = fmt.Sprintf("inspect %s: %v", target, targetErr)
		} else if ok, reason := reuseStagedRelease(context.Background(), staging, gitSHA, tarballPath, verifier); !ok {
			why = reason
		} else if err := os.Rename(stagedDir, target); err != nil {
			why = fmt.Sprintf("move the staged tree into place: %v", err)
		} else {
			reused = true
			// The marker is staging metadata; the installed release never
			// carries it.
			if err := removeStagedInstallMarker(releasesRoot, gitSHA); err != nil {
				_, _ = fmt.Fprintf(stderr, "gregalectl release install: %v\n", err)
				return 3
			}
			_, _ = fmt.Fprintln(stderr, "gregalectl release install: reusing the release verified and extracted during preparation (ADR-20261123150000)")
		}
		if !reused {
			_, _ = fmt.Fprintf(stderr, "gregalectl release install: not reusing the staged release (%s); installing from the tarball\n", why)
		}
	}
	if !reused {
		if err := installViaTarballWithVerifier(releasesRoot, gitSHA, tarballPath, verifier); err != nil {
			_, _ = fmt.Fprintf(stderr, "gregalectl release install: %v\n", err)
			return 3
		}
	}
	// Whatever happened, this release's stage has served its purpose.
	if err := os.RemoveAll(stagedDir); err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl release install: discard staged %s: %v\n", gitSHA, err)
	}
	return -1
}

// stageRelease is --stage-only. It never touches the releases root.
func stageRelease(releasesRoot, staging, gitSHA, tarballPath string, verifier releaseinstall.CosignVerifier, stdout, stderr io.Writer) int {
	skip := func(why string) int {
		_, _ = fmt.Fprintf(stderr, "gregalectl release install: not staging %s: %s; activation installs it as before\n", gitSHA, why)
		jsonEmit(stdout, map[string]any{"git_sha": gitSHA, "staged": false, "skipped": why})
		return 0
	}
	current, err := releaseinstall.CurrentGitSHA(releasesRoot)
	if err != nil {
		return skip(fmt.Sprintf("cannot read the active release: %v", err))
	}
	if current == gitSHA {
		return skip("it is the active release")
	}
	if _, err := os.Lstat(releaseinstall.BundleRoot(releasesRoot, gitSHA)); err == nil {
		return skip("it is already installed on this host")
	} else if !errors.Is(err, os.ErrNotExist) {
		return skip(fmt.Sprintf("cannot inspect the releases root: %v", err))
	}

	// One stage at a time: anything else in the staging area is an aborted
	// rollout's leftover (or debris from an interrupted stage).
	if err := os.MkdirAll(staging, 0o755); err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl release install: create %s: %v\n", staging, err)
		return 3
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl release install: read %s: %v\n", staging, err)
		return 3
	}
	for _, entry := range entries {
		if entry.Name() == gitSHA || entry.Name() == releaseInstallLockName {
			continue
		}
		if err := os.RemoveAll(filepath.Join(staging, entry.Name())); err != nil {
			_, _ = fmt.Fprintf(stderr, "gregalectl release install: discard stale stage %s: %v\n", entry.Name(), err)
			return 3
		}
	}

	if ok, _ := reuseStagedRelease(context.Background(), staging, gitSHA, tarballPath, verifier); ok {
		jsonEmit(stdout, map[string]any{"git_sha": gitSHA, "staged": true, "reused": true})
		return 0
	}
	if err := os.RemoveAll(releaseinstall.BundleRoot(staging, gitSHA)); err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl release install: discard partial stage: %v\n", err)
		return 3
	}
	if err := installViaTarballWithVerifier(staging, gitSHA, tarballPath, verifier); err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl release install: %v\n", err)
		return 3
	}
	m, err := releaseinstall.Read(staging, gitSHA)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl release install: read staged manifest: %v\n", err)
		return 3
	}
	if err := releaseinstall.Verify(staging, m); err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl release install: verify staged release: %v\n", err)
		return 3
	}
	if err := writeStagedInstallMarker(staging, gitSHA, m); err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl release install: record staged release: %v\n", err)
		return 3
	}
	jsonEmit(stdout, map[string]any{"git_sha": gitSHA, "staged": true, "reused": false})
	return 0
}

// reuseStagedRelease reports whether the tree staged under root (the staging
// area) can stand in for installViaTarballWithVerifier, with the reason when
// it cannot.
func reuseStagedRelease(ctx context.Context, root, gitSHA, tarballPath string, verifier releaseinstall.CosignVerifier) (bool, string) {
	body, err := os.ReadFile(stagedInstallMarkerPath(root, gitSHA))
	if errors.Is(err, os.ErrNotExist) {
		return false, "the stage did not finish"
	}
	if err != nil {
		return false, fmt.Sprintf("read marker: %v", err)
	}
	var marker stagedInstallMarker
	if err := json.Unmarshal(body, &marker); err != nil {
		return false, fmt.Sprintf("decode marker: %v", err)
	}
	if marker.Format != stagedInstallMarkerFormat {
		return false, fmt.Sprintf("marker format %d, want %d", marker.Format, stagedInstallMarkerFormat)
	}
	if marker.GitSHA != gitSHA {
		return false, fmt.Sprintf("marker names %s, not %s", marker.GitSHA, gitSHA)
	}

	// The bytes handed to this install must be the bytes the stage verified.
	tarball, sig, sbom, err := canonicalTripleDigests(tarballPath)
	if err != nil {
		return false, fmt.Sprintf("hash the supplied triple: %v", err)
	}
	if tarball != marker.TarballSHA256 || sig != marker.SignatureSHA256 || sbom != marker.SBOMSHA256 {
		return false, "the supplied release triple differs from the staged one"
	}
	// And the retained copies `doctor --deep` re-verifies must still be them.
	retained, err := retainedTripleDigests(root, gitSHA)
	if err != nil {
		return false, err.Error()
	}
	if retained[releaseTarballName] != marker.TarballSHA256 || retained[releaseSigName] != marker.SignatureSHA256 || retained[releaseSBOMName] != marker.SBOMSHA256 {
		return false, "a retained asset changed since preparation"
	}

	m, err := releaseinstall.Read(root, gitSHA)
	if err != nil {
		return false, fmt.Sprintf("read the staged manifest: %v", err)
	}
	digest, err := releaseinstall.SignedManifestDigest(m)
	if err != nil {
		return false, fmt.Sprintf("digest the staged manifest: %v", err)
	}
	if digest != marker.SignedManifestDigest {
		return false, "the extracted manifest is not the one preparation verified"
	}
	// A damaged binary must send activation to the full install, which
	// repairs the tree, not fail every retry.
	if err := releaseinstall.Verify(root, m); err != nil {
		return false, fmt.Sprintf("staged binaries: %v", err)
	}

	// Keep the load-bearing trust bit at activation: cosign on the exact
	// bytes, and the identity it reports must be the one on the manifest.
	bundle := releaseinstall.BundleRoot(root, gitSHA)
	identity, err := verifier.VerifyBlob(ctx, filepath.Join(bundle, releaseTarballName), filepath.Join(bundle, releaseSigName))
	if err != nil {
		return false, fmt.Sprintf("cosign: %v", err)
	}
	if identity == "" || identity != m.Signature || identity != marker.Identity {
		return false, fmt.Sprintf("cosign identity %q does not match the staged manifest's %q", identity, m.Signature)
	}
	return true, ""
}
