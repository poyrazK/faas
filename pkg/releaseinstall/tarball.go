// tarball.go — canonical daemon tarball producer + verifier extractor
// (ADR-113 canonical daemon tarball, PR-A).
//
// A "canonical tarball" is the verifiable bit of a Gregale release: a
// tar+gz of the per-release bin/ tree, paired with a cosign signature
// bundle and a SPDX-2.3 SBoM (PR-A commit 2 + 3 add those). This
// file owns ONLY commit 1: tar+gz Build, plus a no-op-shaped Verify /
// Extract skeleton so PR-A's three commits wire end-to-end.
//
// Load-bearing invariants:
//
//  1. The tarball is BYTE-STABLE across rebuilds. mtimes are pinned
//     to a stable epoch (1970-01-01 UTC), uid/gid to 0, mode to
//     0o755. There is exactly one entry per daemon binary, named
//     after the daemon — no parent directories preserved (the
//     tarball mirrors BinDir's flat shape).
//  2. Every daemon in the canonical catalog
//     (`manifest.SortedHostKeys()`) appears in the tarball.
//     Anything else is rejected at Build time.
//  3. The Manifest embedded in the tarball is built by the existing
//     `Build` pipeline — the tarball IS the per-release directory,
//     just packed; the manifest extracted from it must validate
//     identically to the on-disk one.
//
// The cosign wrapper (commit 2) and the SBoM baseline (commit 3) sit
// on top of the bytes produced here. Verify and Extract are
// signatures only in this commit; their behaviour is fully wired in
// commits 2 and 3.
package releaseinstall

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// Tarball is the canonical artifact for one Gregale release.
//
// GitSHA is the directory-name key. Packed is the tar+gzip bytes;
// Sig and SBOM are populated by commits 2 and 3 (left empty for
// commit 1). Manifest is the on-disk-anchor shape built by the
// existing Build() — the same bytes that would live in
// release-manifest.json under the per-release directory.
type Tarball struct {
	GitSHA     string
	Manifest   Manifest
	Packed     []byte // tar+gzip bytes
	Sig        []byte // cosign bundle; populated by PR-A commit 2
	SBOM       []byte // SPDX-2.3 JSON; populated by PR-A commit 3
	BinSHA256  map[string]string
	ToolSHA256 map[string]string
}

// ErrTarballTampered is the verifier's fail-closed signal: the
// extracted bytes do not match what the manifest advertised. Tested
// by TestTarball_Verify_RejectsTampered (commit 2).
var ErrTarballTampered = errors.New("releaseinstall: tarball tampered")

// stableEpoch is the mtime every tar entry is stamped with. Pinned
// so two builds of the same bin/ produce byte-identical Packed bytes
// (the load-bearing invariant TestTarball_Build_HashStable asserts).
//
// 1970-01-01 00:00:00 UTC; tar will emit this as "00000000000".
var stableEpoch = time.Unix(0, 0).UTC()

// BuildTarball reads the per-release bin directory at <root>/<git-sha>/bin,
// hashes every daemon via the canonical SortedHostKeys() catalog,
// tar+gz's the tree deterministically, embeds the on-disk Manifest,
// and returns a *Tarball ready for Verify / Extract.
//
// Commit 1: this is the producer-side surface only. Commit 2 wires
// cosign sign-blob onto Packed and populates Sig. Commit 3 wires
// `syft` and populates SBOM. Verify / Extract are stitched in along
// with their respective wire-in.
//
// The signature was kept narrow on purpose: there is one bin dir per
// git_sha; BuildTarball is the only point that has to know about it.
func BuildTarball(root, gitSHA, manifestHash string, now time.Time) (*Tarball, error) {
	m, err := Build(root, gitSHA, manifestHash, now)
	if err != nil {
		return nil, fmt.Errorf("releaseinstall: tarball build manifest: %w", err)
	}
	// The manifest is part of the signed tarball, so a wall-clock creation
	// time would make otherwise identical builds produce different bytes.
	// Keep the legacy Build API's audit timestamp, but use the canonical
	// release epoch for the immutable artifact and its on-disk manifest.
	m.CreatedAt = stableEpoch

	bin := BinDir(root, gitSHA)
	daemonNames := manifest.SortedHostKeys()
	// Build a sorted-by-name map to populate BinSHA256; the manifest
	// already ordered by SortedHostKeys() but we want a flat map for
	// downstream consumers (signer, SBoM emitter).
	binHashes := make(map[string]string, len(daemonNames))
	for _, name := range daemonNames {
		hex, ok := strings.CutPrefix(m.DaemonHashes[name], "sha256:")
		if !ok || len(hex) != 64 {
			return nil, fmt.Errorf("releaseinstall: tarball manifest hash for %s missing/wrong shape", name)
		}
		binHashes[name] = hex
	}
	toolHashes := make(map[string]string, len(m.ToolHashes))
	toolNames := make([]string, 0, len(m.ToolHashes))
	for name, value := range m.ToolHashes {
		hex, ok := strings.CutPrefix(value, "sha256:")
		if !ok || len(hex) != 64 {
			return nil, fmt.Errorf("releaseinstall: tarball tool hash for %s missing/wrong shape", name)
		}
		toolHashes[name] = hex
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)
	assetNames := make([]string, 0, len(m.AssetHashes))
	for name := range m.AssetHashes {
		assetNames = append(assetNames, name)
	}
	sort.Strings(assetNames)
	allNames := append(append(append([]string(nil), daemonNames...), toolNames...), assetNames...)
	manifestBody, err := encodeTarballManifest(m)
	if err != nil {
		return nil, fmt.Errorf("releaseinstall: tarball manifest encode: %w", err)
	}

	packed, err := tarGzBin(bin, allNames, map[string][]byte{ManifestName: manifestBody})
	if err != nil {
		return nil, fmt.Errorf("releaseinstall: tarball pack: %w", err)
	}

	return &Tarball{
		GitSHA:     gitSHA,
		Manifest:   m,
		Packed:     packed,
		BinSHA256:  binHashes,
		ToolSHA256: toolHashes,
	}, nil
}

// tarGzBin builds the byte-stable tarball for the given bin dir.
// Iterates `daemonNames` (already sorted by manifest.SortedHostKeys)
// in order, writes each entry with mode 0o755, uid/gid 0, mtime =
// stableEpoch. The resulting `[]byte` is what Tarball.Packed holds.
//
// Errors out (a) if the bin directory is missing, (b) if any
// daemon-named file is not readable. Any file in bin/ NOT in the
// daemon-name catalog is IGNORED (matches `Verify`'s "unexpected
// files" pass at install time — the producer is not the gate; the
// verifier is).
func tarGzBin(bin string, daemonNames []string, extraEntries map[string][]byte) ([]byte, error) {
	info, err := os.Stat(bin)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", bin, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", bin)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, name := range daemonNames {
		binPath, entryName, resolveErr := resolveTarEntry(bin, name)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve %s: %w", name, resolveErr)
		}
		body, err := os.ReadFile(binPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", binPath, err)
		}
		hdr := &tar.Header{
			// Preserve the actual executable filename in the canonical
			// tarball. The manifest key remains the logical name; the
			// extracted tree must be runnable by the systemd units.
			Name:     entryName,
			Mode:     0o755,
			Uid:      0,
			Gid:      0,
			Size:     int64(len(body)),
			ModTime:  stableEpoch,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("tar header %s: %w", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			return nil, fmt.Errorf("tar body %s: %w", name, err)
		}
	}
	extraNames := make([]string, 0, len(extraEntries))
	for name := range extraEntries {
		extraNames = append(extraNames, name)
	}
	sort.Strings(extraNames)
	for _, name := range extraNames {
		body := extraEntries[name]
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Uid:      0,
			Gid:      0,
			Size:     int64(len(body)),
			ModTime:  stableEpoch,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("tar header %s: %w", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			return nil, fmt.Errorf("tar body %s: %w", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

func resolveTarEntry(bin, name string) (string, string, error) {
	if IsRuntimeAssetName(name) {
		return filepath.Join(bin, filepath.FromSlash(name)), name, nil
	}
	path, err := resolveBinary(bin, name)
	return path, filepath.Base(path), err
}

// Verify is the consumer-side counter to Build. PR-A commit 1
// exposes the SHA256 walk over the manifest entries (i.e.,
// redaction check). PR-A commit 2 wires the cosign signature
// verification through the verifier seam. CVE-baseline gating
// (commit 3) is layered on top.
//
// Returns ErrTarballTampered wrapped with the offending daemon name
// when any tarball entry's sha256 disagrees with the manifest.
// Returns whatever the CosignVerifier returns on cosign failure
// (typically ErrCosignSigMissing on a tarball missing Sig).
//
// verifier is required. The signature is the trust bit PR-A's
// tier-2 ship-blocker (#597) adds; a tarball-shaped Manifest.Verify
// with no verifier is a fail-closed shell used by whitebox tests
// to focus on the hash-walk half. To run the cosign path in
// tests, build with FixtureCosignVerifier.
func (t *Tarball) Verify(ctx context.Context, verifier CosignVerifier) (string, error) {
	if t == nil {
		return "", errors.New("releaseinstall: nil tarball")
	}
	if verifier == nil {
		return "", errors.New("releaseinstall: Verify requires a non-nil CosignVerifier (commit 2 invariant)")
	}
	if err := ValidateManifest(t.Manifest); err != nil {
		return "", fmt.Errorf("releaseinstall: tarball manifest: %w", err)
	}

	// 1. SHA-256 walk. PR-A commit 1's surface, delegated to the
	// hashWalk helper.
	if err := t.hashWalk(); err != nil {
		return "", err
	}

	// 2. Cosign sig verify. PR-A commit 2's load-bearing trust
	// bit. Note: the verifier expects the tarball BYTES; in PR-A
	// we hand it a temp file written from Packed (cosign CLI
	// inspects files, not stdin — load-bearing change vs the
	// TUF-style in-memory verifier).
	if len(t.Sig) == 0 {
		return "", ErrCosignSigMissing
	}
	sigTmp, err := os.CreateTemp("", "release-cosign-sig-*.bundle")
	if err != nil {
		return "", fmt.Errorf("releaseinstall: cosign sig tmp: %w", err)
	}
	sigTmpPath := sigTmp.Name()
	defer func() { _ = os.Remove(sigTmpPath) }()
	if _, err := sigTmp.Write(t.Sig); err != nil {
		_ = sigTmp.Close()
		return "", fmt.Errorf("releaseinstall: cosign sig tmp write: %w", err)
	}
	if err := sigTmp.Close(); err != nil {
		return "", fmt.Errorf("releaseinstall: cosign sig tmp close: %w", err)
	}
	tarTmp, err := os.CreateTemp("", "release-tarball-*.tgz")
	if err != nil {
		return "", fmt.Errorf("releaseinstall: cosign tarball tmp: %w", err)
	}
	tarTmpPath := tarTmp.Name()
	defer func() { _ = os.Remove(tarTmpPath) }()
	if _, err := tarTmp.Write(t.Packed); err != nil {
		_ = tarTmp.Close()
		return "", fmt.Errorf("releaseinstall: cosign tarball tmp write: %w", err)
	}
	if err := tarTmp.Close(); err != nil {
		return "", fmt.Errorf("releaseinstall: cosign tarball tmp close: %w", err)
	}
	identity, err := verifier.VerifyBlob(ctx, tarTmpPath, sigTmpPath)
	if err != nil {
		return "", fmt.Errorf("releaseinstall: cosign: %w", err)
	}
	// Stamp the cert identity onto the manifest's reserved
	// Signature field so audit trails see who signed it. The
	// struct's Signature field has been reserved-empty since
	// PR-3.5 (commit 1 comment at bundle.go:54-57); commit 2
	// populates it.
	t.Manifest.Signature = identity
	return identity, nil
}

// Extract unpacks the tarball into <root>/<git-sha>/bin/ — the same
// path the legacy copyBinIntoRelease wrote. The atomic-symlink flip
// that follows (pkg/releaseinstall.AtomicFlip) is unchanged; ADR-113
// only swaps the producer surface.
//
// Idempotent: a second call with the same bytes writes identical
// files. Tested by TestTarball_Extract_Idempotent.
func (t *Tarball) Extract(root string) error {
	if t == nil {
		return errors.New("releaseinstall: nil tarball")
	}
	if err := ValidateManifest(t.Manifest); err != nil {
		return fmt.Errorf("releaseinstall: extract validate: %w", err)
	}

	bin := BinDir(root, t.GitSHA)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return fmt.Errorf("releaseinstall: extract mkdir %s: %w", bin, err)
	}

	gz, err := gzip.NewReader(bytes.NewReader(t.Packed))
	if err != nil {
		return fmt.Errorf("releaseinstall: gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	allowedEntries := trustedArchiveEntries(t.Manifest)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("releaseinstall: extract next: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// The manifest is a root-level metadata member, not a binary under
		// <release>/bin. Verify already checked its hash and the installer
		// writes the trusted, signature-stamped manifest separately.
		if hdr.Name == ManifestName {
			// tar.Reader.Next discards unread bytes from the current
			// member before advancing, so there is no need to copy an
			// attacker-sized metadata payload into io.Discard here.
			continue
		}
		// Never use the archive-controlled name as a filesystem path. The
		// manifest has already been validated against the fixed daemon,
		// support-binary, and runtime-asset catalogs; use only the matching
		// trusted value from that allowlist. This also rejects traversal,
		// absolute, symlink-shaped, and otherwise unexpected entries before
		// filepath.Join or any write operation.
		safe, ok := allowedEntries[hdr.Name]
		if !ok {
			return fmt.Errorf("%w: unexpected archive entry %q", ErrTarballTampered, hdr.Name)
		}
		// Post-join containment defence: reject a pre-existing
		// symlinked bin directory and ensure every nested parent
		// resolves to the intended release tree before writing.
		binInfo, lstatErr := os.Lstat(bin)
		if lstatErr != nil {
			return fmt.Errorf("releaseinstall: lstat bin: %w", lstatErr)
		}
		if binInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: bin directory is a symlink", ErrTarballTampered)
		}
		binReal, evalErr := filepath.EvalSymlinks(bin)
		if evalErr != nil {
			return fmt.Errorf("releaseinstall: eval bin symlinks: %w", evalErr)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("releaseinstall: extract read %s: %w", hdr.Name, err)
		}
		outPath := filepath.Join(bin, filepath.FromSlash(safe))
		parent := filepath.Dir(outPath)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("releaseinstall: extract mkdir %s: %w", parent, err)
		}
		parentReal, evalErr := filepath.EvalSymlinks(parent)
		if evalErr != nil {
			return fmt.Errorf("releaseinstall: eval extract parent: %w", evalErr)
		}
		expectedParent := filepath.Join(binReal, filepath.Dir(filepath.FromSlash(safe)))
		if parentReal != filepath.Clean(expectedParent) {
			return fmt.Errorf("%w: tarball entry %q escapes bin dir (%s)",
				ErrTarballTampered, hdr.Name, parentReal)
		}
		if info, err := os.Lstat(outPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: tarball entry %q targets a symlink", ErrTarballTampered, hdr.Name)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("releaseinstall: lstat extract path %s: %w", outPath, err)
		}
		if err := replaceFile(outPath, body, 0o755); err != nil {
			return fmt.Errorf("releaseinstall: extract write %s: %w", outPath, err)
		}
	}
	return nil
}

// replaceFile publishes body at path through a temp file and a rename, never
// by writing through an existing file. A no-drain rollout keeps the active
// release's daemons running through the install, so re-installing that
// release (a rerun after a later step failed) finds its binaries executing:
// writing through one fails with ETXTBSY (v0.1.18-rc.312, fsn-3). The rename
// leaves a running process on its old inode.
func replaceFile(path string, body []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// trustedArchiveEntries returns the only archive names Extract may write.
// Values are copied from the validated release catalog and manifest rather
// than from tar headers, so the archive cannot taint a filesystem path even
// when it contains an unexpected or malicious entry name.
func trustedArchiveEntries(m Manifest) map[string]string {
	entries := make(map[string]string, len(m.DaemonHashes)+len(m.ToolHashes)+len(m.AssetHashes)+len(manifest.SortedHostKeys()))
	for _, logical := range manifest.SortedHostKeys() {
		entries[logical] = logical
		if canonical := executableName(logical); canonical != logical {
			entries[canonical] = canonical
		}
	}
	for name := range m.ToolHashes {
		entries[name] = name
	}
	for name := range m.AssetHashes {
		entries[name] = name
	}
	// A signed pre-canonical artifact may contain guest-init without a
	// tool_hashes entry. It is a fixed legacy compatibility member, not a
	// wildcard for unrecognised archive paths.
	for _, name := range LegacyUnhashedSupportBinaryNames() {
		if _, ok := m.ToolHashes[name]; !ok {
			entries[name] = name
		}
	}
	return entries
}

// ReadManifest re-loads the per-release manifest at
// <root>/<git-sha>/release-manifest.json and overwrites
// t.Manifest with its contents. The companion to Extract: after
// the tarball has been unpacked into bin/, the on-disk manifest
// (which is what AtomicFlip + Verify on disk actually reads) MUST
// agree with t.Manifest (which is what Verify stamped the
// Signature onto). PR-B's doctor verify-tarball-sbom probe uses
// ReadManifest to give the operator a single source of truth
// across the tarball-side and disk-side state.
//
// Errors:
//   - manifest file missing → wraps os.IsNotExist error
//   - JSON malformed or ValidateManifest fails → wraps the error
func (t *Tarball) ReadManifest(root string) error {
	if t == nil {
		return errors.New("releaseinstall: nil tarball")
	}
	if t.GitSHA == "" {
		return errors.New("releaseinstall: tarball missing git_sha")
	}
	m, err := Read(root, t.GitSHA)
	if err != nil {
		return fmt.Errorf("releaseinstall: read manifest: %w", err)
	}
	t.Manifest = m
	return nil
}

// Signature returns the cosign cert identity stamped onto
// t.Manifest by Verify. Empty string means Verify has not run
// (or the tarball was constructed without going through the
// canonical cosign path). PR-B's doctor verify-tarball-sbom
// probe uses this getter for the per-release "signature=<identity>"
// line in the JSON output. Returned by-value so callers can't
// mutate the underlying Manifest.Signature field.
func (t *Tarball) Signature() string {
	if t == nil {
		return ""
	}
	return t.Manifest.Signature
}

// SafeArchiveRelativeName validates a tar.Header.Name and returns a
// path-safe, slash-separated relative path. The returned string is
// the ONLY value the caller may pass to filepath.Join / os.WriteFile.
//
// CodeQL (go/zipslip, CWE-22) recognises the explicit string-prefix
// + substring guards below as taint barriers: the data flow from
// hdr.Name to a filesystem sink is severed at this function's
// return value. Returning `clean` only when every guard passes
// (and returning an error otherwise) is the canonical CodeQL
// "sanitize-then-use" pattern.
//
// Rules (defence in depth, all must pass):
//
//  1. Name is non-empty.
//  2. Name does not start with '/' or a Windows drive letter.
//  3. Name contains no parent-traversal segment ("..").
//  4. The cleaned path is non-empty, non-root, and relative.
//
// Exported so callers building canary-side tar validators can reuse
// the same CodeQL-recognised barrier.
func SafeArchiveRelativeName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("archive entry has empty name")
	}
	// Path-traversal patterns CodeQL flags: leading '/', a '..'
	// segment anywhere in the path, Windows backslash absolute
	// paths, and Windows drive-letter prefixes (which would
	// resolve to absolute on Windows even though filepath.Base
	// silently strips them on Linux/macOS).
	if strings.HasPrefix(name, "/") ||
		strings.Contains(name, `\`) ||
		// Windows drive letter: a single ASCII letter followed
		// by ':'. CodeQL's go/zipslip flags this pattern on
		// cross-platform analysis; the regex is the
		// recognised barrier.
		(len(name) >= 2 && name[1] == ':' &&
			((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z'))) {
		return "", fmt.Errorf("archive entry %q contains path-traversal segment", name)
	}
	clean := path.Clean(name)
	if clean == "." || clean == "" || path.IsAbs(clean) {
		return "", fmt.Errorf("archive entry %q has unsafe relative path", name)
	}
	for _, segment := range strings.Split(clean, "/") {
		if segment == ".." {
			return "", fmt.Errorf("archive entry %q contains path-traversal segment", name)
		}
	}
	return clean, nil
}

// SafeArchiveEntryName is the legacy basename-only view of
// SafeArchiveRelativeName. Keep it for callers that only accept flat
// catalog names; extraction uses SafeArchiveRelativeName so nested runtime
// assets retain their directory structure.
func SafeArchiveEntryName(name string) (string, error) {
	relative, err := SafeArchiveRelativeName(name)
	if err != nil {
		return "", err
	}
	base := filepath.Base(filepath.FromSlash(relative))
	if base == "." || base == "/" || base == `\` || base == "" {
		return "", fmt.Errorf("archive entry %q has unsafe basename", name)
	}
	return base, nil
}

// hashWalk runs the manifest-sha256-walk half of Verify against
// t.Packed. Returns nil on success; ErrTarballTampered wrapped on
// mismatch (or "tarball has files the catalog doesn't recognise,
// which is also tampering"). Extracted so callers that want
// hash-only checks (e.g., a doctor probe that doesn't need the
// cosign signature half) can call it without spinning up the
// full Tarball.Verify machinery.
func (t *Tarball) hashWalk() error {
	entryHashes, err := tarballEntryHashes(t.Packed)
	if err != nil {
		return err
	}
	daemonNames := manifest.SortedHostKeys()
	for _, name := range daemonNames {
		wantHex, ok := t.BinSHA256[name]
		if !ok {
			return fmt.Errorf("%w: manifest missing daemon %s", ErrTarballTampered, name)
		}
		gotHex, ok := entryHashes[name]
		if !ok {
			gotHex, ok = entryHashes[executableName(name)]
		}
		if !ok {
			return fmt.Errorf("%w: tarball missing daemon %s", ErrTarballTampered, name)
		}
		if gotHex != wantHex {
			return fmt.Errorf("%w: %s sha256=%s want %s", ErrTarballTampered, name, gotHex, wantHex)
		}
	}
	for name, wantHex := range t.ToolSHA256 {
		gotHex, ok := entryHashes[name]
		if !ok {
			return fmt.Errorf("%w: tarball missing tool %s", ErrTarballTampered, name)
		}
		if gotHex != wantHex {
			return fmt.Errorf("%w: %s sha256=%s want %s", ErrTarballTampered, name, gotHex, wantHex)
		}
	}
	for name, want := range t.Manifest.AssetHashes {
		gotHex, ok := entryHashes[name]
		if !ok {
			return fmt.Errorf("%w: tarball missing asset %s", ErrTarballTampered, name)
		}
		wantHex := strings.TrimPrefix(want, "sha256:")
		if gotHex != wantHex {
			return fmt.Errorf("%w: %s sha256=%s want %s", ErrTarballTampered, name, gotHex, wantHex)
		}
	}
	manifestBody, err := encodeTarballManifest(t.Manifest)
	if err != nil {
		return fmt.Errorf("%w: encode manifest: %w", ErrTarballTampered, err)
	}
	gotManifest, ok := entryHashes[ManifestName]
	if !ok {
		return fmt.Errorf("%w: tarball missing %s", ErrTarballTampered, ManifestName)
	}
	if gotManifest != sha256Hex(manifestBody) {
		return fmt.Errorf("%w: %s sha256=%s want %s", ErrTarballTampered, ManifestName, gotManifest, sha256Hex(manifestBody))
	}
	roster := make(map[string]struct{}, len(daemonNames)+len(t.ToolSHA256))
	for _, n := range daemonNames {
		roster[n] = struct{}{}
		if canonical := executableName(n); canonical != n {
			roster[canonical] = struct{}{}
		}
	}
	for name := range t.ToolSHA256 {
		roster[name] = struct{}{}
	}
	for name := range t.Manifest.AssetHashes {
		roster[name] = struct{}{}
	}
	for _, name := range LegacyUnhashedSupportBinaryNames() {
		if _, ok := t.ToolSHA256[name]; !ok {
			roster[name] = struct{}{}
		}
	}
	roster[ManifestName] = struct{}{}
	var unknown []string
	for name := range entryHashes {
		if _, ok := roster[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("%w: tarball contains non-catalog files: %s", ErrTarballTampered, strings.Join(unknown, ", "))
	}
	return nil
}

// tarballEntryHashes walks the packed tarball and returns the sha256
// of every regular-file member keyed by its basename. Wraps every
// tar/gzip parse error in ErrTarballTampered: a tarball that this
// package produced must always parse, so a parse failure is treated
// as tampering (defence against a host that constructed a tarball
// the verifier can't read).
func tarballEntryHashes(packed []byte) (map[string]string, error) {
	out := make(map[string]string)
	gz, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, fmt.Errorf("%w: gzip reader: %w", ErrTarballTampered, err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: tar next: %w", ErrTarballTampered, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("%w: tar read %s: %w", ErrTarballTampered, hdr.Name, err)
		}
		name := filepath.ToSlash(filepath.Clean(hdr.Name))
		if name == "." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || strings.HasPrefix(name, "/") {
			return nil, fmt.Errorf("%w: unsafe tar entry %q", ErrTarballTampered, hdr.Name)
		}
		out[name] = sha256Hex(body)
	}
	return out, nil
}
