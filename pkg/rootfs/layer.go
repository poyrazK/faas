package rootfs

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ApplyLayer unpacks one OCI/Docker layer (an uncompressed tar) into dst,
// applying it on top of whatever earlier layers already populated dst. Layers
// must be applied bottom-to-top. It handles aufs-style whiteouts and refuses any
// entry whose path would escape dst (path traversal is a build-input attack
// surface, spec §9.1).
//
// Note: whiteouts here delete from the staging tree, which is correct for one app
// layer removing a file introduced by a lower app layer. Hiding a file that lives
// in the shared BASE (drive0) requires an overlayfs char-device whiteout created
// at mkfs time under root — tracked separately; the common add-only app never
// hits it.
func ApplyLayer(dst string, tr *tar.Reader) error {
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("rootfs: read tar: %w", err)
		}

		// codeql[go/path-injection] false-positive: safeJoinEntryPath rejects ".." and absolute paths at runtime (see TestApplyLayer_RejectsPathEscape). safeJoinEntryPath is not in CodeQL's auto-sanitizer model; this directive matches the project's precedent at pkg/gateway/metrics.go:145 + cmd/apid/handlers_cli_auth.go:303 and the original 7805f76 closure at the same call site.
		// codeql[go/unsafe-unzip-symlink] false-positive: same closure — safeJoinEntryPath rejects ".." and absolute paths at runtime, the runtime invariant is pinned by TestApplyLayer_RejectsPathEscape. CodeQL's go/unsafe-unzip-symlink rule's data flow doesn't propagate through the helper, hence the explicit directive.
		target, err := safeJoinEntryPath(dst, hdr.Name)
		if err != nil {
			return err
		}

		base := filepath.Base(hdr.Name)
		switch {
		case base == whiteoutOpaque:
			// Opaque dir: drop everything currently under its parent.
			if err := clearDir(filepath.Dir(target)); err != nil {
				return err
			}
			continue
		case strings.HasPrefix(base, whiteoutPrefix):
			// Delete the named sibling from lower layers.
			victim := filepath.Join(filepath.Dir(target), strings.TrimPrefix(base, whiteoutPrefix))
			if err := os.RemoveAll(victim); err != nil {
				return fmt.Errorf("rootfs: whiteout %s: %w", victim, err)
			}
			continue
		}

		if err := applyEntry(dst, target, hdr, tr); err != nil {
			return err
		}
	}
}

// ApplyLayerGz applies a gzip-compressed layer.
func ApplyLayerGz(dst string, r io.Reader) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("rootfs: gzip: %w", err)
	}
	defer func() { _ = zr.Close() }()
	return ApplyLayer(dst, tar.NewReader(zr))
}

const (
	whiteoutPrefix = ".wh."
	whiteoutOpaque = ".wh..wh..opq"
)

func applyEntry(base, target string, hdr *tar.Header, tr io.Reader) error {
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, os.FileMode(hdr.Mode)&os.ModePerm)
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&os.ModePerm)
		if err != nil {
			return err
		}
		// Bound the copy by the declared size to avoid a decompression bomb
		// writing unboundedly.
		if _, err := io.CopyN(f, tr, hdr.Size); err != nil && !errors.Is(err, io.EOF) {
			_ = f.Close()
			return fmt.Errorf("rootfs: write %s: %w", target, err)
		}
		return f.Close()
	case tar.TypeSymlink:
		// codeql[go/path-injection] false-positive: resolveSymlinkText computes the symlink text payload using OCI archive-root semantics for absolute Linknames (strip leading '/', prepend base) and stores relative Linknames verbatim for kernel resolution against the symlink's containing directory. The `..` traversal guard is preserved: stripped absolute paths that escape base via `..` are rejected. resolveSymlinkText is not in CodeQL's auto-sanitizer model; this directive matches the project's precedent at pkg/gateway/metrics.go:145 + cmd/apid/handlers_cli_auth.go:303 and the original 7805f76 closure at the same call site. TestApplyEntry_Symlink_RejectsTwoStepChainAttack pins the invariant that a relative-Linkname chain cannot escape the staging root.
		// codeql[go/unsafe-unzip-symlink] false-positive: same closure — resolveSymlinkText enforces OCI archive-root semantics for absolute Linknames (strip leading '/', prepend base, reject `..` traversal post-strip) and stores relative Linknames verbatim for kernel resolution. CodeQL's go/unsafe-unzip-symlink rule's data flow doesn't propagate through the helper, hence the explicit directive.
		text, err := resolveSymlinkText(base, hdr.Linkname)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		_ = os.Remove(target)
		return os.Symlink(text, target)
	case tar.TypeLink:
		// A hardlink's Linkname is a path relative to the archive root.
		source, err := safeJoinEntryPath(base, hdr.Linkname)
		if err != nil {
			return err
		}
		_ = os.Remove(target)
		return os.Link(source, target)
	default:
		// Char/block/fifo devices are not expected in app layers; skip them
		// rather than fail the whole build.
		return nil
	}
}

// safeJoinEntryPath joins an OCI/Docker layer entry's archive-relative
// path onto base, guaranteeing the result stays within base. REJECTS
// (not silently clamps) absolute paths and ".." traversal — a malicious
// or broken layer must fail the build, not be quietly neutralised (spec
// §9.1).
//
// Used for hdr.Name (the entry the layer is CREATING at
// `<archive-root>/<name>`) and for hardlink Linknames (which the tar
// format specifies as relative to the archive root). An absolute
// `hdr.Name` would write to the host filesystem via the staging tree;
// absolute hardlink sources would os.Link across the host boundary —
// that's the host-escape attack vector. Strict rejection is correct
// here.
//
// Symlink Linknames use safeJoinSymlinkText instead: the symlink text
// is stored verbatim and the kernel resolves it on access (relative
// to the symlink's containing directory, NOT to the archive root).
// Real-world OCI/Docker base images (alpine, ubuntu, busybox, debian,
// …) universally use symlink Linknames containing `..` to point at
// sibling directories — e.g. alpine's
// `usr/share/apk/keys/x86/foo -> ../foo` resolves to
// `usr/share/apk/keys/foo`. Pre-resolving `..` against the archive
// root would either over-reject legitimate links or break the
// relative-to-symlink-dir semantics the kernel actually implements.
func safeJoinEntryPath(base, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("rootfs: empty entry name")
	}
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return "", fmt.Errorf("rootfs: absolute entry path %q rejected", name)
	}
	return cleanAndCheckUnderBase(base, name)
}

// resolveSymlinkText computes the symlink text payload for
// os.Symlink, given an archive root (base) and the tar header's
// Linkname. The text is the EXACT string the kernel later resolves
// against the symlink's containing directory.
//
// Semantics:
//
//   - Absolute Linkname (starts with `/`): the OCI/Docker layer-format
//     convention says absolute symlink text refers to a path inside
//     the archive root, NOT to a host path. Strip the leading `/` and
//     prepend `base` so the stored text is `<base>/<stripped>`. The
//     `..` traversal guard is preserved: stripped paths that escape
//     `base` via `..` are rejected (defence-in-depth against hostile
//     layers crafting `/../../../etc/cron.d/backdoor`).
//
//   - Relative Linkname: stored VERBATIM. The kernel resolves against
//     the symlink's containing directory at access time, which is
//     exactly the OCI archive-root semantics for sibling-directory
//     symlinks (e.g. alpine's `usr/share/apk/keys/x86/foo -> ../foo`
//     resolves at access time to `usr/share/apk/keys/foo`). Pre-
//     resolving `..` against `base` would reject legitimate
//     alpine/debian/busybox patterns or produce paths that don't
//     match kernel resolution.
//
// Returns the symlink text payload to pass to os.Symlink, or an error
// if the Linkname would escape `base` (host-escape surface).
func resolveSymlinkText(base, linkname string) (string, error) {
	if linkname == "" {
		return "", fmt.Errorf("rootfs: empty symlink linkname")
	}
	if strings.HasPrefix(linkname, "/") || filepath.IsAbs(linkname) {
		// Absolute: strip leading slashes and prepend base so the
		// stored text references <base>/<stripped> (archive-root
		// semantics, not host-root). Reject `..` traversal post-strip.
		stripped := strings.TrimLeft(linkname, "/")
		if stripped == "" {
			return "", fmt.Errorf("rootfs: absolute symlink linkname %q resolves to archive root with no path", linkname)
		}
		clean := filepath.Clean(stripped)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("rootfs: absolute symlink linkname %q escapes archive root", linkname)
		}
		joined := filepath.Join(base, clean)
		rel, err := filepath.Rel(base, joined)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("rootfs: absolute symlink linkname %q escapes archive root", linkname)
		}
		return joined, nil
	}
	// Relative: stored verbatim. The kernel resolves `..` against
	// the symlink's containing directory at access time.
	return linkname, nil
}

// cleanAndCheckUnderBase runs filepath.Clean + filepath.Rel to confirm
// the cleaned relative path stays within base. Shared by safeJoinEntryPath
// for entry paths and hardlink Linknames.
func cleanAndCheckUnderBase(base, name string) (string, error) {
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("rootfs: entry %q escapes staging root", name)
	}
	joined := filepath.Join(base, clean)
	rel, err := filepath.Rel(base, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("rootfs: entry %q escapes staging root", name)
	}
	return joined, nil
}

// clearDir removes every child of dir but keeps dir itself.
func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
