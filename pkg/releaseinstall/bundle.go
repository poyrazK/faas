// Package releaseinstall owns the cluster-shipped release bundle:
// the daemon-binary tree at /opt/faas/releases/<git-sha>/ that the
// renderer (PR-2) and the doctor (PR-4) verify against.
//
// This is a sibling package to pkg/releasebundle/ (the per-deploy
// function-code bundle). The two concepts were named too close for
// a single package — see the PR-3 plan at
// /Users/poyrazk/.claude/plans/crispy-hopping-sphinx.md for the
// rationale and the upstream ADR-110 description.
package releaseinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/releasebundle"
)

// FormatVersion is the on-disk manifest format version. Bump when
// the Manifest shape changes in a non-backward-compatible way.
const FormatVersion = 1

// ManifestName is the on-disk manifest filename (relative to the
// release root). Mirrors pkg/releasebundle.ManifestName for the
// per-deploy concept.
const ManifestName = "release-manifest.json"

// BinDirName is the per-release directory holding the daemon
// binaries. /opt/faas/releases/<git-sha>/bin/<daemon>.
const BinDirName = "bin"

// Manifest is the JSON shape stored at <root>/release-manifest.json.
// It is the on-disk anchor for the cluster-shipped release bundle.
//
// Immutable fields: GitSHA, ManifestHash, FormatVersion. Mutable
// across rebuilds: CreatedAt (always now()), DaemonHashes (depends
// on which daemons the build produced binaries for).
type Manifest struct {
	FormatVersion int               `json:"format_version"`
	GitSHA        string            `json:"git_sha"`
	ManifestHash  string            `json:"manifest_hash"`
	DaemonHashes  map[string]string `json:"daemon_hashes"`          // daemon name -> "sha256:<64hex>"
	ToolHashes    map[string]string `json:"tool_hashes,omitempty"`  // required host support executable -> "sha256:<64hex>"
	AssetHashes   map[string]string `json:"asset_hashes,omitempty"` // nested release asset path -> "sha256:<64hex>"
	CreatedAt     time.Time         `json:"created_at"`
	// Signature is reserved for a future PR-3.5 cosign verification
	// pass. Always empty for PR-3. Kept on the struct so the JSON
	// shape is stable when signing lands.
	Signature string `json:"signature,omitempty"`
}

// BinDir returns the absolute path to the per-release bin directory.
// /opt/faas/releases/<git-sha>/bin.
//
// The gitSHA is the directory name verbatim. /opt/faas/releases/<id>/
// convention is locked in by pkg/deploycontroller.Config
// (cmd/deployctl/main.go:266/307) and the dryrun audit at
// pkg/deploycontroller/dryrun.go:111.
func BinDir(root, gitSHA string) string {
	return filepath.Join(root, gitSHA, BinDirName)
}

// BundleRoot returns the per-release directory.
// /opt/faas/releases/<git-sha>/.
func BundleRoot(root, gitSHA string) string {
	return filepath.Join(root, gitSHA)
}

// Build walks the bin directory for the given gitSHA, hashes each
// daemon binary, and produces a Manifest. The daemon-name keys
// come from manifest.SortedHostKeys() — the canonical daemon-name
// catalog — so the JSON map is byte-stable across rebuilds.
//
// returned Manifest has CreatedAt set to now; validate it with
// ValidateManifest before writing.
//
// daemonHint is the explicit binary-name suffix the manifest schema
// expects (e.g. "vmmd" looks for a binary named "vmmd" at
// root/<git-sha>/bin/vmmd). A missing daemon is a hard error: a
// shipped bundle MUST cover every daemon in the catalog, otherwise
// the doctor (PR-4) reports "missing binary for <daemon>" on every
// box that pulls this release.
//
// The faas-tunnel denylist from issue #911 / PR-5 is reused: a
// binary whose name contains "faas-tunnel" (case-insensitive) is
// rejected at bundle-build time, not install time. Mirrors the
// pkg/releasebundle.IsForbiddenPath guard.
func Build(root, gitSHA, manifestHash string, now time.Time) (Manifest, error) {
	if root == "" {
		return Manifest{}, errors.New("releaseinstall: empty root")
	}
	if err := ValidateBundleInputs(gitSHA, manifestHash); err != nil {
		return Manifest{}, err
	}

	bin := BinDir(root, gitSHA)
	daemonNames := manifest.SortedHostKeys()
	hashes := make(map[string]string, len(daemonNames))
	for _, name := range daemonNames {
		if releasebundle.IsForbiddenPath(name) {
			// Refuse to ship a daemon with faas-tunnel in its name.
			// PR-5's denylist applies to binaries too.
			return Manifest{}, fmt.Errorf("releaseinstall: forbidden daemon name %q (issue #911 denylist: faas-tunnel)", name)
		}
		binPath, resolveErr := resolveBinary(bin, name)
		if resolveErr != nil {
			return Manifest{}, fmt.Errorf("releaseinstall: resolve %s: %w", name, resolveErr)
		}
		hash, err := hashFile(binPath)
		if err != nil {
			return Manifest{}, fmt.Errorf("releaseinstall: hash %s: %w", binPath, err)
		}
		hashes[name] = "sha256:" + hash
	}
	tools := make(map[string]string)
	for _, name := range SupportBinaryNames() {
		binPath := filepath.Join(bin, name)
		info, statErr := os.Stat(binPath)
		if errors.Is(statErr, os.ErrNotExist) {
			// Older producers did not ship support executables. Keep
			// Build backwards-compatible; new bundles include and
			// Verify gates every support file they are given.
			continue
		}
		if statErr != nil {
			return Manifest{}, fmt.Errorf("releaseinstall: stat tool %s: %w", binPath, statErr)
		}
		if !info.Mode().IsRegular() {
			return Manifest{}, fmt.Errorf("releaseinstall: tool %s is not a regular file", binPath)
		}
		hash, hashErr := hashFile(binPath)
		if hashErr != nil {
			return Manifest{}, fmt.Errorf("releaseinstall: hash tool %s: %w", binPath, hashErr)
		}
		tools[name] = "sha256:" + hash
	}
	assets := make(map[string]string)
	if info, statErr := os.Stat(filepath.Join(bin, "runners")); statErr == nil && info.IsDir() {
		for _, name := range sortedRuntimeAssetNames() {
			path := filepath.Join(bin, filepath.FromSlash(name))
			info, statErr := os.Stat(path)
			if statErr != nil {
				return Manifest{}, fmt.Errorf("releaseinstall: stat runtime asset %s: %w", name, statErr)
			}
			if !info.Mode().IsRegular() {
				return Manifest{}, fmt.Errorf("releaseinstall: runtime asset %s is not a regular file", name)
			}
			hash, hashErr := hashFile(path)
			if hashErr != nil {
				return Manifest{}, fmt.Errorf("releaseinstall: hash runtime asset %s: %w", name, hashErr)
			}
			assets[name] = "sha256:" + hash
		}
	}

	return Manifest{
		FormatVersion: FormatVersion,
		GitSHA:        gitSHA,
		ManifestHash:  manifestHash,
		DaemonHashes:  hashes,
		ToolHashes:    tools,
		AssetHashes:   assets,
		CreatedAt:     now.UTC(),
	}, nil
}

// ValidateBundleInputs validates the two operator-supplied identity fields
// before a bundle command touches the filesystem. It intentionally does not
// call ValidateManifest: created_at and daemon_hashes do not exist until the
// binaries have been staged and hashed.
func ValidateBundleInputs(gitSHA, manifestHash string) error {
	if gitSHA == "" {
		return errors.New("releaseinstall: empty git_sha")
	}
	if manifestHash == "" {
		return errors.New("releaseinstall: empty manifest_hash")
	}
	if !validGitSHA(gitSHA) {
		return fmt.Errorf("releaseinstall: git_sha %q is not a 40-char lowercase hex", gitSHA)
	}
	if !validManifestHash(manifestHash) {
		return fmt.Errorf("releaseinstall: manifest_hash %q is not sha256:<64hex>", manifestHash)
	}
	return nil
}

// Write atomically writes the manifest to <root>/<git-sha>/release-manifest.json
// using the same tmp-then-rename pattern as pkg/releasebundle.Write.
// Tmp file lives in the target's parent directory so the rename is
// on the same filesystem (no cross-device rename).
func Write(root string, manifest Manifest) error {
	if err := ValidateManifest(manifest); err != nil {
		return err
	}
	body, err := encodeManifest(manifest)
	if err != nil {
		return fmt.Errorf("releaseinstall: marshal manifest: %w", err)
	}
	dir := BundleRoot(root, manifest.GitSHA)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("releaseinstall: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, ManifestName)
	tmp, err := os.CreateTemp(dir, ".release-manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("releaseinstall: create manifest temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("releaseinstall: chmod manifest temp: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("releaseinstall: write manifest temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("releaseinstall: sync manifest temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("releaseinstall: close manifest temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("releaseinstall: publish manifest: %w", err)
	}
	return nil
}

// encodeManifest is the canonical byte representation of the manifest.
// Both the on-disk release directory and the canonical tarball use these
// bytes so the installer can compare the embedded and extracted manifests
// without accepting two different encodings of the same release metadata.
func encodeManifest(manifest Manifest) ([]byte, error) {
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

// encodeTarballManifest returns the immutable manifest representation that is
// embedded in and signed with the canonical tarball. Signature is deliberately
// excluded: the installer learns the CI certificate identity only after
// verifying the tarball and stamps that audit value onto the on-disk manifest.
func encodeTarballManifest(manifest Manifest) ([]byte, error) {
	manifest.Signature = ""
	return encodeManifest(manifest)
}

// Read loads and validates the manifest at <root>/<git-sha>/release-manifest.json.
func Read(root, gitSHA string) (Manifest, error) {
	path := filepath.Join(BundleRoot(root, gitSHA), ManifestName)
	body, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("releaseinstall: read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, fmt.Errorf("releaseinstall: decode manifest: %w", err)
	}
	if err := ValidateManifest(m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Verify checks that every daemon binary in the bin directory
// matches the manifest's recorded hash. Returns nil on success.
// This is the dogfood for the doctor (PR-4) — the install path
// reads the manifest and verifies before flipping the symlink.
func Verify(root string, m Manifest) error {
	if err := ValidateManifest(m); err != nil {
		return err
	}
	bin := BinDir(root, m.GitSHA)
	if err := verifyCanonicalFiles(bin, m); err != nil {
		return err
	}
	// A controller-managed host release contains the canonical daemon
	// bundle plus deployctl/migrate and the role-specific systemd tree.
	// Those controller files are intentionally outside the signed
	// releaseinstall bin catalog; the sibling releasebundle manifest is
	// the exact-file integrity envelope for that assembled deployment.
	// Keep the canonical hash checks above, then let that envelope account
	// for the additional files. A plain releaseinstall directory has no
	// manifest.json and keeps the strict catalog walk below.
	if hasDeploymentManifest, err := verifyDeploymentBundle(root, m); hasDeploymentManifest {
		return err
	}

	return verifyUnexpectedBinFiles(bin, m)
}

// verifyCanonicalFiles checks every daemon, support executable, and runtime
// asset recorded in the releaseinstall manifest. It deliberately does not
// walk for unexpected files because controller-managed deployments carry a
// second, role-specific integrity envelope around the canonical tree.
func verifyCanonicalFiles(bin string, m Manifest) error {
	// Iterate over the canonical daemon-name set so an extra
	// binary in bin/ that isn't in the catalog doesn't silently
	// pass verification.
	daemonNames := manifest.SortedHostKeys()
	for _, name := range daemonNames {
		want, ok := m.DaemonHashes[name]
		if !ok {
			return fmt.Errorf("releaseinstall: manifest missing daemon %s", name)
		}
		binPath, resolveErr := resolveBinary(bin, name)
		if resolveErr != nil {
			return fmt.Errorf("releaseinstall: resolve %s: %w", name, resolveErr)
		}
		got, err := hashFile(binPath)
		if err != nil {
			return fmt.Errorf("releaseinstall: hash %s: %w", binPath, err)
		}
		if "sha256:"+got != want {
			return fmt.Errorf("releaseinstall: daemon %s sha256 %s, want %s", name, "sha256:"+got, want)
		}
	}
	for name, want := range m.ToolHashes {
		if !IsReleaseBinaryName(name) || IsCatalogBinaryName(name) {
			return fmt.Errorf("releaseinstall: manifest contains unknown tool %s", name)
		}
		got, err := hashFile(filepath.Join(bin, name))
		if err != nil {
			return fmt.Errorf("releaseinstall: hash tool %s: %w", name, err)
		}
		if "sha256:"+got != want {
			return fmt.Errorf("releaseinstall: tool %s sha256 %s, want %s", name, "sha256:"+got, want)
		}
	}
	for name, want := range m.AssetHashes {
		path := filepath.Join(bin, filepath.FromSlash(name))
		got, err := hashFile(path)
		if err != nil {
			return fmt.Errorf("releaseinstall: hash asset %s: %w", name, err)
		}
		if "sha256:"+got != want {
			return fmt.Errorf("releaseinstall: asset %s sha256 %s, want %s", name, "sha256:"+got, want)
		}
	}
	return nil
}

// verifyDeploymentBundle verifies the optional releasebundle envelope used
// by deployctl's assembled host deployment. The bool distinguishes a plain
// canonical release (no envelope) from a malformed envelope, which must be
// reported as an error rather than silently falling back to the strict bin
// walk.
func verifyDeploymentBundle(root string, m Manifest) (bool, error) {
	releaseRoot := BundleRoot(root, m.GitSHA)
	path := filepath.Join(releaseRoot, releasebundle.ManifestName)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("releaseinstall: stat deployment manifest: %w", err)
	}
	if !info.Mode().IsRegular() {
		return true, fmt.Errorf("releaseinstall: deployment manifest is not a regular file")
	}
	deployment, err := releasebundle.Read(releaseRoot)
	if err != nil {
		return true, fmt.Errorf("releaseinstall: read deployment manifest: %w", err)
	}
	if deployment.ReleaseID != m.GitSHA || deployment.CommitSHA != m.GitSHA {
		return true, fmt.Errorf("releaseinstall: deployment manifest identity (%s, %s) does not match git_sha %s", deployment.ReleaseID, deployment.CommitSHA, m.GitSHA)
	}
	// KGV rotation writes an operator-owned baseline after the signed host
	// bundle is assembled. The baseline has its own strict JSON validation;
	// permit that exact regular-file sidecar without weakening verification
	// for any other unexpected path.
	if err := releasebundle.VerifyWithAllowedFiles(releaseRoot, deployment, SBOMBaselineName); err != nil {
		return true, fmt.Errorf("releaseinstall: verify deployment bundle: %w", err)
	}
	return true, nil
}

// verifyUnexpectedBinFiles retains the strict catalog check for canonical
// release directories that do not have a controller deployment envelope.
func verifyUnexpectedBinFiles(bin string, m Manifest) error {
	// Walk the bin directory and reject any file that isn't a
	// catalog daemon (names from manifest.SortedHostKeys()).
	// Mirrors pkg/releasebundle.Verify's "unexpected files" check.
	daemonNames := manifest.SortedHostKeys()
	catalog := make(map[string]struct{}, len(daemonNames)*2)
	for _, name := range daemonNames {
		catalog[name] = struct{}{}
		if canonical := executableName(name); canonical != name {
			catalog[canonical] = struct{}{}
		}
	}
	for name := range m.ToolHashes {
		catalog[name] = struct{}{}
	}
	for name := range m.AssetHashes {
		catalog[name] = struct{}{}
	}
	// Older signed releases may have left guest-init in the release
	// directory before it was added to tool_hashes. Preserve only this
	// explicitly catalogued compatibility file; arbitrary leftovers remain
	// rejected below.
	for _, name := range LegacyUnhashedSupportBinaryNames() {
		if _, err := os.Stat(filepath.Join(bin, name)); err == nil {
			catalog[name] = struct{}{}
		}
	}
	walkRoot := bin
	if info, err := os.Lstat(bin); err == nil && info.Mode()&os.ModeSymlink != 0 {
		walkRoot, err = filepath.EvalSymlinks(bin)
		if err != nil {
			return fmt.Errorf("releaseinstall: resolve bin symlink: %w", err)
		}
	}
	var unexpected []string
	err := filepath.WalkDir(walkRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(walkRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if _, ok := catalog[rel]; !ok {
			unexpected = append(unexpected, rel)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("releaseinstall: walk bin: %w", err)
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		return fmt.Errorf("releaseinstall: unexpected bin files: %s", strings.Join(unexpected, ", "))
	}
	return nil
}

// ValidateManifest checks the manifest against the contract documented
// in the type comment. Called from Write, Read, and Verify.
func ValidateManifest(m Manifest) error {
	if m.FormatVersion != FormatVersion {
		return fmt.Errorf("releaseinstall: unsupported format version %d", m.FormatVersion)
	}
	if !validGitSHA(m.GitSHA) {
		return fmt.Errorf("releaseinstall: git_sha %q is not a 40-char lowercase hex", m.GitSHA)
	}
	if !validManifestHash(m.ManifestHash) {
		return fmt.Errorf("releaseinstall: manifest_hash %q is not sha256:<64hex>", m.ManifestHash)
	}
	if m.CreatedAt.IsZero() {
		return errors.New("releaseinstall: created_at is zero")
	}
	if len(m.DaemonHashes) != len(manifest.SortedHostKeys()) {
		return fmt.Errorf("releaseinstall: daemon_hashes has %d entries, want %d (every catalog daemon must be present)",
			len(m.DaemonHashes), len(manifest.SortedHostKeys()))
	}
	// Confirm every daemon in the catalog is present and the rest
	// of the values are well-formed ("sha256:" + 64 hex chars).
	daemonNames := manifest.SortedHostKeys()
	for _, name := range daemonNames {
		v, ok := m.DaemonHashes[name]
		if !ok {
			return fmt.Errorf("releaseinstall: daemon_hashes missing %s", name)
		}
		if !validDaemonHash(v) {
			return fmt.Errorf("releaseinstall: daemon %s hash %q is not sha256:<64hex>", name, v)
		}
	}
	for name, value := range m.ToolHashes {
		if !IsReleaseBinaryName(name) || IsCatalogBinaryName(name) {
			return fmt.Errorf("releaseinstall: tool_hashes contains unknown tool %s", name)
		}
		if !validDaemonHash(value) {
			return fmt.Errorf("releaseinstall: tool %s hash %q is not sha256:<64hex>", name, value)
		}
	}
	for name, value := range m.AssetHashes {
		if !IsRuntimeAssetName(name) {
			return fmt.Errorf("releaseinstall: asset_hashes contains unknown asset %s", name)
		}
		if !validDaemonHash(value) {
			return fmt.Errorf("releaseinstall: asset %s hash %q is not sha256:<64hex>", name, value)
		}
	}
	return nil
}

// hashFile reads the file at path and returns hex-encoded SHA256
// (64 lowercase chars). Mirrors the function in pkg/releasebundle —
// intentionally duplicated rather than re-exported to keep the
// per-deploy boundary clean.
func hashFile(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return sha256Hex(body), nil
}

// sha256Hex returns hex-encoded SHA256 of body (64 lowercase chars).
// Extracted so the tarball producer (PR-A commit 1) can hash in-memory
// tar entries without round-tripping to disk, and so hashFile stays
// the canonical "what's the digest of this file" predicate.
func sha256Hex(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

func validGitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if isHexLower(r) {
			continue
		}
		return false
	}
	return true
}

// ValidGitSHA is the exported form of validGitSHA. Callers that
// want to gate a 40-char lowercase hex git_sha (e.g. the doctor
// command's per-node release_id checks) should use this so the
// predicate stays in one place. PR-3 had to keep the helper
// unexported because the only consumer was inside the package;
// PR-4 doctor is the first external caller.
func ValidGitSHA(s string) bool { return validGitSHA(s) }

func validManifestHash(s string) bool {
	if len(s) != 7+64 {
		return false
	}
	if !strings.HasPrefix(s, "sha256:") {
		return false
	}
	return validDaemonHash(s)
}

func validDaemonHash(s string) bool {
	if len(s) != 7+64 {
		return false
	}
	if !strings.HasPrefix(s, "sha256:") {
		return false
	}
	for _, r := range s[7:] {
		if isHexLower(r) {
			continue
		}
		return false
	}
	return true
}

// isHexLower reports whether r is an ASCII lowercase hex digit.
// Extracted so validGitSHA / validDaemonHash both share the same
// check without tripping QF1001's "could apply De Morgan's law".
func isHexLower(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
}
