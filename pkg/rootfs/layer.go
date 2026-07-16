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

		target, err := safeJoin(dst, hdr.Name)
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
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		_ = os.Remove(target)
		return os.Symlink(hdr.Linkname, target)
	case tar.TypeLink:
		// A hardlink's Linkname is a path relative to the archive root.
		source, err := safeJoin(base, hdr.Linkname)
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

// safeJoin joins name onto base and guarantees the result stays within base,
// REJECTING (not silently clamping) absolute paths and ".." traversal — a
// malicious or broken layer must fail the build, not be quietly neutralised
// (spec §9.1).
func safeJoin(base, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("rootfs: empty entry name")
	}
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return "", fmt.Errorf("rootfs: absolute entry path %q rejected", name)
	}
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("rootfs: entry %q escapes staging root", name)
	}
	joined := filepath.Join(base, clean)
	// Defence in depth: confirm the final path is still under base.
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
