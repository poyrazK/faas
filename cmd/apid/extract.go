package main

// extract.go — Phase 3 tarball extraction seam.
//
// distinct from cmd/apid/deploy_inputs.go::validateTarballShape, which
// only inspects the gzip header stream and never touches disk. The
// scanner path needs the bytes on disk because reposcan.Scan takes an
// fs.FS (os.DirFS(root)), not an io.Reader. The two helpers coexist:
// `validateAndSpool` runs first (compressed cap, file-count cap,
// symlink-name escape) and `extractTarGzToDir` runs second (total
// expanded cap, per-entry size cap, full entry-type allow-list).
//
// every entry-type rejection is fail-closed because a malicious
// tarball that writes to /dev/null or unlinks /etc/passwd would be
// catastrophic; the package-level cap keeps the disk cost bounded;
// the scrub dir is created with 0o700 and removed via defer.

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// scanSpoolRoot returns the directory under which per-request
// extraction scratch dirs are created. env-overridable for tests so
// they can keep the spool under t.TempDir() without touching
// /var/spool/faas.
const scanSpoolRootEnv = "FAAS_SCAN_SPOOL_ROOT"

func scanSpoolRoot() string {
	if v := os.Getenv(scanSpoolRootEnv); v != "" {
		return v
	}
	return "/var/spool/faas/scans"
}

// extractLimits caps what extractTarGzToDir will unpack. The defaults
// match the §4 plan cap (SourceTarballMaxMB) × 2.5 for the expanded
// total (ADR-050 §3). Per-entry cap stops a single 10 GB entry
// inside a small compressed envelope. File-count cap is the same
// 10 000 as the source-deploy path so customers see a consistent
// envelope across the two surfaces.
type extractLimits struct {
	MaxEntries    int   // default 10_000
	MaxFileBytes  int64 // default 256 MiB
	MaxTotalBytes int64 // default = compressed cap × 2.5
}

// defaultExtractLimits returns the limits for a given plan. Both
// compressed and expanded caps live in api.Limits — we scale the
// compressed cap by 2.5× to give customers headroom for already-tar
// sources. The constants are intentionally not literal here so a
// future limit table edit propagates without a sync point.
func defaultExtractLimits(l api.Limits) extractLimits {
	compressed := int64(l.SourceTarballMaxMB) * 1024 * 1024
	return extractLimits{
		MaxEntries:    10_000,
		MaxFileBytes:  256 * 1024 * 1024,
		MaxTotalBytes: compressed*2 + compressed/2, // 2.5x
	}
}

// extractTarGzToDir unpacks src into a freshly-created directory
// under scanSpoolRoot() and returns the dir path. The dir is removed
// by the caller (use defer os.RemoveAll). On error the partial
// directory is cleaned up before returning — no half-unpacked tarballs
// leak to the next request.
//
// Rules (mirrors the §11 hardening posture):
//   - Reject absolute paths and `..` segments (already done by
//     validateTarballShape, but we re-check defensively in case the
//     caller skipped that step).
//   - Reject TypeLink, TypeSymlink, TypeChar, TypeBlock, TypeFifo.
//     A symlink whose target is inside the archive root is the
//     classic "exfil a host file" vector — never allow.
//   - Reject entry counts beyond MaxEntries.
//   - Reject any single entry whose body exceeds MaxFileBytes
//     (read with io.LimitReader so a hostile tarball can't pin
//     apid's memory).
//   - Reject cumulative expanded bytes beyond MaxTotalBytes.
func extractTarGzToDir(src string, lim extractLimits) (string, *api.Problem) {
	if err := os.MkdirAll(scanSpoolRoot(), 0o700); err != nil {
		return "", api.ErrCapacity("could not create scan spool dir")
	}
	id := randomToken(12)
	dst := filepath.Join(scanSpoolRoot(), id)
	if err := os.Mkdir(dst, 0o700); err != nil {
		return "", api.ErrCapacity("could not create scan dir")
	}
	if prob := extractTarGzInto(src, dst, lim); prob != nil {
		_ = os.RemoveAll(dst)
		return "", prob
	}
	return dst, nil
}

// pathStaysUnder returns true iff `target` is lexically inside
// `root`, including the case where target == root. Mirrors the
// defensive guard at pkg/rootfs/layer.go:148-165 (safeJoin). It does
// not defend against pre-existing symlink ancestors; dst is freshly
// created (0o700) by extractTarGzToDir and extraction rejects all
// symlink/hardlink entry types, so the lexical check is sufficient.
//
// Contract: the post-`filepath.Rel` result is rejected only when it
// is `".."` or starts with `"../"`. Every other value is inside
// root — including `"."` (target == root), a nested path like
// `"foo/bar"`, and the empty string (the only `Rel` can produce in
// practice for inputs that resolve identically on every OS; harmless
// to accept). `filepath.Rel` errors out only on cases that imply
// containment failure (different volumes on Windows, etc.) and we
// treat those as escapes too.
//
// Callers must pass `target` and `root` after `filepath.Clean`. Both
// come out of `filepath.Join` already cleaned, so the production
// call site (extractTarGzInto below) is fine; a future caller that
// builds paths some other way must clean first.
//
// This is the post-Join belt-and-braces guard against a
// customer-supplied archive that slipped past escapesArchiveRoot
// upstream. The original guard `!filepath.IsLocal(filepath.Dir(target))
// && filepath.Dir(target) != dst` was wrong: filepath.IsLocal is
// relative to process cwd, so an absolute dst produced a false
// `IsLocal == false` for every nested entry on any box whose
// FAAS_SCAN_SPOOL_ROOT is absolute (every Linux box; every Mac dev
// box). Replaced after the bug surfaced during the issue #432
// phase 5 local validation on Mac (post-flip path-filter PR #521).
func pathStaysUnder(target, root string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func extractTarGzInto(src, dst string, lim extractLimits) *api.Problem {
	//nolint:forbidigo // src is a daemon-spooled path under FAAS_SCAN_SPOOL_ROOT
	// (set by scanService above); not a customer-supplied path. The
	// lint tripwire that catches customer-path os.Open calls lives in
	// cmd/gregale; this path is not reachable from a customer file.
	f, err := os.Open(src)
	if err != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid,
			"Bad source", err.Error())
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid,
			"Not gzip", "source must be tar.gz")
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	var (
		entries int
		total   int64
		// firstDir is the single top-level prefix the tarball must
		// share (tar wraps every entry under `<root>/...`). We
		// strip it on write so reposcan.Scan sees a clean root
		// directory. Set on the first non-empty-name header.
		firstDir string
		firstSet bool
	)
	for {
		// codeql[go/path-injection] false-positive: escapesArchiveRoot
		// (line ~150) rejects every ".." and absolute hdr.Name before
		// the value reaches any filesystem operation below. The
		// post-Join filepath.IsLocal check at line ~207 is the
		// belt-and-braces runtime guard. `dst` is a daemon-owned 0o700
		// scratch dir, never customer-controllable. CodeQL's taint
		// engine does not trace escapesArchiveRoot as a sanitizer
		// (same precedent as pkg/rootfs/layer.go:35).
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid,
				"Bad tar", err.Error())
		}
		// Reject every named escape — mirrors escapesArchiveRoot.
		// The tar name walks up the parent dir if joined under any
		// root; we keep the same predicate the deploy path uses so
		// both surfaces fail the same set of inputs.
		if hdr.Name == "" {
			continue
		}
		if escapesArchiveRoot(hdr.Name) {
			return api.ErrSourceInvalid("absolute paths or '..' entries are rejected")
		}
		// Reject every type that could exfiltrate or escape. TypeReg
		// and TypeRegA are the only safe ones; TypeDir is allowed
		// so a tarball can carry directory entries (Railpack-style
		// archives do).
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeDir:
			// allowed
		default:
			return api.ErrSourceInvalid(
				fmt.Sprintf("entry type %d not allowed (only files/dirs)", hdr.Typeflag))
		}
		entries++
		if entries > lim.MaxEntries {
			return api.ErrSourceInvalid(
				fmt.Sprintf("too many files (>%d)", lim.MaxEntries))
		}

		// Strip the leading "<root>/" prefix on the first
		// non-empty-name header. tar archives the customer uploaded
		// usually have this; fs.MapFS test fixtures don't. The
		// no-prefix case (single root already) leaves firstDir="".
		// Canonicalise harmless leading "./" segments before selecting
		// and stripping the archive wrapper. Without this, an entry such
		// as "./repo/compose.yaml" selected "." as the wrapper and left
		// the real repository root nested one level too deep, producing an
		// empty project plan. escapesArchiveRoot above already rejected
		// absolute names and parent traversal. Preserve a trailing slash on
		// directory headers because wrapper discovery uses that separator.
		name := hdr.Name
		for strings.HasPrefix(name, "./") {
			name = strings.TrimPrefix(name, "./")
		}
		if !firstSet {
			// Only consume the first segment as the archive root
			// if every other entry also begins with it. We can't
			// know that yet on the first header, so we tentatively
			// set and re-strip; on a mismatch the path becomes
			// root-relative (still safe — escapesArchiveRoot
			// already cleared the security check).
			if i := strings.IndexByte(name, '/'); i >= 0 {
				firstDir = name[:i]
			}
			firstSet = true
		}
		if firstDir != "" && strings.HasPrefix(name, firstDir+"/") {
			name = name[len(firstDir)+1:]
		}
		if name == "" {
			// the root directory entry itself; skip
			continue
		}

		target := filepath.Join(dst, filepath.FromSlash(name))
		// final defensive containment check after Join — confirms
		// the joined target stays under `dst`. Mirrors the
		// safeJoin predicate at pkg/rootfs/layer.go:148-165.
		// `filepath.IsLocal` would answer "is this path relative to
		// process cwd?" — the wrong question here, since absolute
		// spool dirs (`/var/spool/faas/scans/<id>`) make every nested
		// post-Join path absolute and IsLocal returns false, breaking
		// every nested entry. `filepath.Rel(dst, target)` is the
		// correct lexical-containment predicate: a relative result of
		// ".." or "../*" means the target escaped; "."
		// (`target == dst`) and any non-traversing rel are inside.
		// codeql[go/path-injection] false-positive: escapesArchiveRoot
		// rejected any ".." or absolute path upstream (line ~158),
		// and `dst` is a daemon-owned 0o700 scratch dir under
		// FAAS_SCAN_SPOOL_ROOT — never customer-controllable. The
		// post-Join filepath.Rel check below is the belt-and-braces
		// runtime guard (same precedent as pkg/rootfs/layer.go:35 and
		// pkg/rootfs/build.go:541; CodeQL's taint engine doesn't
		// trace filepath.Rel as a sanitizer either — this suppression
		// mirrors the one we already have for filepath.IsLocal).
		if !pathStaysUnder(target, dst) {
			return api.ErrSourceInvalid("path escape after join rejected")
		}

		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return api.ErrCapacity("could not create dir")
			}
			continue
		}

		// File: cap per-entry size and cumulative bytes. Read with
		// io.LimitReader so a hostile entry that claims a 10 GiB
		// body can't pin apid.
		if hdr.Size > lim.MaxFileBytes {
			return api.NewProblem(http.StatusRequestEntityTooLarge,
				api.CodeSourceTooLarge,
				"Entry too large",
				fmt.Sprintf("entry %q is %d bytes; cap is %d", name, hdr.Size, lim.MaxFileBytes))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return api.ErrCapacity("could not create parent dir")
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return api.ErrCapacity("could not create file")
		}
		written, copyErr := io.Copy(out, io.LimitReader(tr, lim.MaxFileBytes))
		_ = out.Close()
		if copyErr != nil {
			return api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid,
				"Bad tar", copyErr.Error())
		}
		total += written
		if total > lim.MaxTotalBytes {
			return api.NewProblem(http.StatusRequestEntityTooLarge,
				api.CodeSourceTooLarge,
				"Source too large",
				fmt.Sprintf("expanded total exceeds %d bytes", lim.MaxTotalBytes))
		}
	}
	return nil
}
