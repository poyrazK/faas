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
// while the node still serves, and records what it staged. Activation's
// ordinary `release install --tarball-path` then reuses that tree instead of
// decompressing the 489 MB bundle three more times on the drained node's
// critical path (32 s per node on rc.371–rc.374).
//
// Reuse never widens trust: activation still re-runs cosign on the exact
// bytes it was handed, requires them to be the bytes preparation verified,
// and the caller's on-disk releaseinstall.Verify still hashes every binary
// against the manifest before the flip. Any mismatch falls back to the full
// install.

const stagedInstallMarkerName = releaseinstall.StagedInstallMarkerName

// stagedInstallMarkerFormat is bumped when the marker's meaning changes; an
// unknown format is never reused.
const stagedInstallMarkerFormat = 1

type stagedInstallMarker struct {
	Format int    `json:"format"`
	GitSHA string `json:"git_sha"`
	// Digests of the canonical triple preparation verified and retained.
	TarballSHA256   string `json:"tarball_sha256"`
	SignatureSHA256 string `json:"signature_sha256"`
	SBOMSHA256      string `json:"sbom_sha256"`
	// SignedManifestDigest ties the extracted manifest to the signed tarball
	// member that hashWalk checked (releaseinstall.SignedManifestDigest).
	SignedManifestDigest string `json:"signed_manifest_digest"`
	// Identity is the cosign certificate identity stamped on the manifest.
	Identity string `json:"identity"`
}

func stagedInstallMarkerPath(releasesRoot, gitSHA string) string {
	return filepath.Join(releaseinstall.BundleRoot(releasesRoot, gitSHA), stagedInstallMarkerName)
}

// removeStagedInstallMarker drops the marker before any full extraction, so a
// tree interrupted mid-extract is never mistaken for a finished stage.
func removeStagedInstallMarker(releasesRoot, gitSHA string) error {
	err := os.Remove(stagedInstallMarkerPath(releasesRoot, gitSHA))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove staged-install marker: %w", err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
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
// sibling cosign bundle and SBoM (same lookup as installViaTarball).
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

// writeStagedInstallMarker records a finished stage. It is called only after
// installViaTarball and the on-disk Verify succeeded, and it is the last write
// of the stage.
func writeStagedInstallMarker(releasesRoot, gitSHA, tarballPath string, m releaseinstall.Manifest) error {
	tarball, sig, sbom, err := canonicalTripleDigests(tarballPath)
	if err != nil {
		return err
	}
	digest, err := releaseinstall.SignedManifestDigest(m)
	if err != nil {
		return fmt.Errorf("staged-install marker: signed manifest digest: %w", err)
	}
	body, err := json.MarshalIndent(stagedInstallMarker{
		Format:               stagedInstallMarkerFormat,
		GitSHA:               gitSHA,
		TarballSHA256:        tarball,
		SignatureSHA256:      sig,
		SBOMSHA256:           sbom,
		SignedManifestDigest: digest,
		Identity:             m.Signature,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("staged-install marker: encode: %w", err)
	}
	dir := releaseinstall.BundleRoot(releasesRoot, gitSHA)
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
	if err := os.Rename(tmpPath, stagedInstallMarkerPath(releasesRoot, gitSHA)); err != nil {
		return fmt.Errorf("staged-install marker: publish: %w", err)
	}
	return nil
}

// installOrReuseStaged runs the tarball half of `release install`. It returns
// -1 when the caller should continue to the on-disk Verify, SBoM gate and
// flip, or the exit code to return now (an error, or a finished
// --stage-only).
func installOrReuseStaged(releasesRoot, gitSHA, tarballPath string, stageOnly bool, verifier releaseinstall.CosignVerifier, stdout, stderr io.Writer) int {
	unlock, err := lockReleaseInstalls(releasesRoot)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl release install: %v\n", err)
		return 3
	}
	defer unlock()
	if stageOnly {
		if skip := stageWouldTouchAnInstalledRelease(releasesRoot, gitSHA); skip != "" {
			_, _ = fmt.Fprintf(stderr, "gregalectl release install: not staging %s: %s; activation installs it as before\n", gitSHA, skip)
			jsonEmit(stdout, map[string]any{"git_sha": gitSHA, "staged": false, "skipped": skip})
			return 0
		}
	}
	reused, why := reuseStagedRelease(context.Background(), releasesRoot, gitSHA, tarballPath, verifier)
	switch {
	case reused:
		_, _ = fmt.Fprintln(stderr, "gregalectl release install: reusing the release verified and extracted during preparation (ADR-20261123150000)")
	default:
		if why != "" {
			_, _ = fmt.Fprintf(stderr, "gregalectl release install: not reusing the staged release (%s); installing from the tarball\n", why)
		}
		if err := installViaTarballWithVerifier(releasesRoot, gitSHA, tarballPath, verifier); err != nil {
			_, _ = fmt.Fprintf(stderr, "gregalectl release install: %v\n", err)
			return 3
		}
	}
	if !stageOnly {
		return -1
	}
	m, err := releaseinstall.Read(releasesRoot, gitSHA)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl release install: read manifest: %v\n", err)
		return 3
	}
	if err := releaseinstall.Verify(releasesRoot, m); err != nil {
		_, _ = fmt.Fprintf(stderr, "gregalectl release install: verify manifest: %v\n", err)
		return 3
	}
	if !reused {
		if err := writeStagedInstallMarker(releasesRoot, gitSHA, tarballPath, m); err != nil {
			_, _ = fmt.Fprintf(stderr, "gregalectl release install: record staged release: %v\n", err)
			return 3
		}
	}
	jsonEmit(stdout, map[string]any{"git_sha": gitSHA, "staged": true, "reused": reused})
	return 0
}

// stageWouldTouchAnInstalledRelease returns why a stage must leave gitSHA
// alone, or "". The marker means "prepared, never activated" to retention, so
// a stage only ever marks a directory it creates (or re-checks its own
// marker): never the active release (a rerun prepare on a node already
// serving it, or a stage that waited on the lock while activation flipped),
// and never a directory an earlier install left without a marker (a rollback
// target). Marking either would let retention prune a rollback target.
func stageWouldTouchAnInstalledRelease(releasesRoot, gitSHA string) string {
	current, err := releaseinstall.CurrentGitSHA(releasesRoot)
	if err != nil {
		return fmt.Sprintf("cannot read the active release: %v", err)
	}
	if current == gitSHA {
		return "it is the active release"
	}
	if _, err := os.Lstat(releaseinstall.BundleRoot(releasesRoot, gitSHA)); err == nil {
		if _, markerErr := os.Lstat(stagedInstallMarkerPath(releasesRoot, gitSHA)); errors.Is(markerErr, os.ErrNotExist) {
			return "an earlier install already placed it on this host"
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Sprintf("cannot inspect the release directory: %v", err)
	}
	return ""
}

// reuseStagedRelease reports whether the release tree staged during
// preparation can stand in for installViaTarball. A false result with an
// empty reason means nothing was staged; a non-empty reason says why a staged
// tree was refused. The caller still runs the on-disk Verify.
func reuseStagedRelease(ctx context.Context, releasesRoot, gitSHA, tarballPath string, verifier releaseinstall.CosignVerifier) (bool, string) {
	body, err := os.ReadFile(stagedInstallMarkerPath(releasesRoot, gitSHA))
	if errors.Is(err, os.ErrNotExist) {
		return false, ""
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

	// The bytes handed to this install must be the bytes preparation verified.
	tarball, sig, sbom, err := canonicalTripleDigests(tarballPath)
	if err != nil {
		return false, fmt.Sprintf("hash the supplied triple: %v", err)
	}
	if tarball != marker.TarballSHA256 || sig != marker.SignatureSHA256 || sbom != marker.SBOMSHA256 {
		return false, "the supplied release triple differs from the staged one"
	}
	// And the retained copies `doctor --deep` re-verifies must still be them.
	bundle := releaseinstall.BundleRoot(releasesRoot, gitSHA)
	for name, want := range map[string]string{
		releaseTarballName: marker.TarballSHA256,
		releaseSigName:     marker.SignatureSHA256,
		releaseSBOMName:    marker.SBOMSHA256,
	} {
		got, err := fileSHA256(filepath.Join(bundle, name))
		if err != nil {
			return false, fmt.Sprintf("hash retained %s: %v", name, err)
		}
		if got != want {
			return false, fmt.Sprintf("retained %s changed since preparation", name)
		}
	}

	m, err := releaseinstall.Read(releasesRoot, gitSHA)
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

	// Keep the load-bearing trust bit at activation: cosign on the exact
	// bytes, and the identity it reports must be the one on the manifest.
	identity, err := verifier.VerifyBlob(ctx, filepath.Join(bundle, releaseTarballName), filepath.Join(bundle, releaseSigName))
	if err != nil {
		return false, fmt.Sprintf("cosign: %v", err)
	}
	if identity == "" || identity != m.Signature || identity != marker.Identity {
		return false, fmt.Sprintf("cosign identity %q does not match the staged manifest's %q", identity, m.Signature)
	}
	return true, ""
}
