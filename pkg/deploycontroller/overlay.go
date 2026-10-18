package deploycontroller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/releasebundle"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

// OverlayRequest describes a per-unit release
// (docs/adr/1650-per-unit-control-plane-releases.md): the active release
// with a few files swapped.
type OverlayRequest struct {
	// ReleaseID names the new release directory. The CD planner derives it
	// from the base release and the unit versions, so a rerun assembles the
	// same directory.
	ReleaseID string
	// ExpectedCurrent is the release id the overlay is built over. Assembly
	// refuses when another release is active.
	ExpectedCurrent string
	CommitSHA       string
	// Files maps release-relative paths (bin/apid, unit-versions.json) to
	// the staged files that replace or add them.
	Files map[string]string
}

// AssembleOverlay installs a per-unit release next to the active one and
// returns its root. Every file the request does not replace is a hard link
// to the active release, so the copy is instant and costs no disk; replaced
// files are written as new inodes, never through a link. The deploy lock is
// held throughout so a concurrent deployment cannot change the active
// release underneath the copy. When the release is already installed and
// verifies, or is already active, it is reused as is.
func (c *Controller) AssembleOverlay(req OverlayRequest) (string, error) {
	if req.ReleaseID == "" || req.ExpectedCurrent == "" || req.CommitSHA == "" || len(req.Files) == 0 {
		return "", errors.New("deploycontroller: incomplete overlay request")
	}
	// A 40-hex id is what retention and rollback recognise as a release.
	if !releaseinstall.ValidGitSHA(req.ReleaseID) || req.ReleaseID == req.ExpectedCurrent {
		return "", fmt.Errorf("deploycontroller: invalid overlay release id %q", req.ReleaseID)
	}
	lock, err := acquireLock(c.config.LockPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.Close() }()

	target := filepath.Join(c.config.ReleasesRoot, req.ReleaseID)
	current, err := readCurrentTarget(c.config.CurrentPath)
	if err != nil {
		return "", fmt.Errorf("deploycontroller: overlay: read current release: %w", err)
	}
	if current == filepath.Clean(target) {
		return target, nil
	}
	if filepath.Base(current) != req.ExpectedCurrent {
		return "", fmt.Errorf("deploycontroller: overlay %q is built over %q, but the active release is %q", req.ReleaseID, req.ExpectedCurrent, filepath.Base(current))
	}
	if manifest, readErr := releasebundle.Read(target); readErr == nil && verifyInstalledRelease(target, manifest) == nil {
		return target, nil
	}
	if err := os.RemoveAll(target); err != nil {
		return "", fmt.Errorf("deploycontroller: overlay: remove incomplete %s: %w", req.ReleaseID, err)
	}

	base, err := baseManifest(current)
	if err != nil {
		return "", fmt.Errorf("deploycontroller: overlay: read active release: %w", err)
	}
	staging, err := os.MkdirTemp(c.config.ReleasesRoot, ".overlay-"+req.ReleaseID+"-")
	if err != nil {
		return "", fmt.Errorf("deploycontroller: overlay: create staging: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := linkTree(current, staging); err != nil {
		return "", fmt.Errorf("deploycontroller: overlay: link active release: %w", err)
	}
	replaced := make([]string, 0, len(req.Files))
	for rel, source := range req.Files {
		if err := replaceFile(filepath.Join(staging, filepath.FromSlash(rel)), source); err != nil {
			return "", fmt.Errorf("deploycontroller: overlay: stage %s: %w", rel, err)
		}
		replaced = append(replaced, rel)
	}
	sort.Strings(replaced)
	manifest, err := releasebundle.Overlay(base, staging, req.ReleaseID, req.CommitSHA, replaced, time.Now().UTC())
	if err != nil {
		return "", err
	}
	if err := releasebundle.Write(staging, manifest); err != nil {
		return "", err
	}
	// MkdirTemp creates the staging root 0700; release roots are 0755.
	if err := os.Chmod(staging, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(staging, target); err != nil {
		return "", fmt.Errorf("deploycontroller: overlay: publish %s: %w", req.ReleaseID, err)
	}
	return target, nil
}

// baseManifest reads the active release's integrity manifest. A compute
// host's release comes from `gregalectl release install`, which writes the
// canonical release-manifest.json but no deployment manifest; give it one,
// built from exactly the files it has, so the overlay can inherit its
// hashes and the controller can verify it as a rollback target. The host's
// SBOM baseline sidecar is lifecycle state, never part of a release.
func baseManifest(root string) (releasebundle.Manifest, error) {
	manifest, err := releasebundle.Read(root)
	if err == nil {
		return manifest, nil
	}
	if _, statErr := os.Stat(filepath.Join(root, releasebundle.ManifestName)); !errors.Is(statErr, os.ErrNotExist) {
		return releasebundle.Manifest{}, err
	}
	id := filepath.Base(root)
	body, err := os.ReadFile(filepath.Join(root, releaseinstall.ManifestName))
	if err != nil {
		return releasebundle.Manifest{}, fmt.Errorf("release %s has neither a deployment nor a canonical manifest: %w", id, err)
	}
	var canonical struct {
		GitSHA string `json:"git_sha"`
	}
	if err := json.Unmarshal(body, &canonical); err != nil || !releaseinstall.ValidGitSHA(canonical.GitSHA) {
		return releasebundle.Manifest{}, fmt.Errorf("release %s canonical manifest names no commit", id)
	}
	built, err := releasebundle.Build(root, id, canonical.GitSHA, "linux/amd64", time.Now().UTC())
	if err != nil {
		return releasebundle.Manifest{}, err
	}
	files := built.Files[:0]
	for _, file := range built.Files {
		if file.Path != releaseinstall.SBOMBaselineName {
			files = append(files, file)
		}
	}
	built.Files = files
	if err := releasebundle.Write(root, built); err != nil {
		return releasebundle.Manifest{}, err
	}
	return built, nil
}

// linkTree hard-links every release file of src into dst, recreating the
// directories. The manifest and the host-owned SBOM baseline sidecar are
// release-specific and are not carried over.
func linkTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		out := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.Mkdir(out, info.Mode().Perm())
		case rel == releasebundle.ManifestName || rel == releaseinstall.SBOMBaselineName:
			return nil
		case !info.Mode().IsRegular():
			return fmt.Errorf("%s is not a regular file", rel)
		default:
			return os.Link(path, out)
		}
	})
}

// replaceFile writes source to path as a new file with source's mode,
// unlinking whatever path named first so a hard link is never written
// through.
func replaceFile(path, source string) error {
	in, err := os.Open(source) //nolint:forbidigo // operator-staged release file
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", source)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// O_CREAT applies the umask; the release records the staged mode.
	return os.Chmod(path, info.Mode().Perm())
}
