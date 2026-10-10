package devpatch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// ErrUnsafeEntry rejects a patch entry that could escape the target
// directory or create anything other than a regular file.
var ErrUnsafeEntry = errors.New("devpatch: unsafe patch entry")

// BuildArchive copies the regular files named in include (archive-relative
// paths) out of a complete gzip-compressed source archive into a new tar.gz.
// Entries are stored relative to sourceRoot, owned by root, with modes masked
// to 0644 or 0755. The result is rejected when it would exceed maxBytes.
func BuildArchive(source io.Reader, sourceRoot string, include map[string]bool, maxBytes int64) ([]byte, string, error) {
	gz, err := gzip.NewReader(source)
	if err != nil {
		return nil, "", fmt.Errorf("devpatch: open source archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
	root := strings.Trim(path.Clean("/"+sourceRoot), "/")
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	tr := tar.NewReader(gz)
	found := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("devpatch: read source archive: %w", err)
		}
		name := strings.Trim(path.Clean("/"+strings.TrimSuffix(hdr.Name, "/")), "/")
		if !include[name] || (hdr.Typeflag != tar.TypeReg && hdr.Typeflag != 0) {
			continue
		}
		rel, inside := Relative(root, name)
		if !inside || rel == "" {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: rel, Typeflag: tar.TypeReg, Mode: maskMode(hdr.Mode), Size: hdr.Size}); err != nil {
			return nil, "", fmt.Errorf("devpatch: write patch header: %w", err)
		}
		if _, err := io.CopyN(tw, tr, hdr.Size); err != nil {
			return nil, "", fmt.Errorf("devpatch: copy %s: %w", rel, err)
		}
		found++
		if int64(out.Len()) > maxBytes {
			return nil, "", fmt.Errorf("devpatch: patch exceeds %d bytes", maxBytes)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	if out.Len() > int(maxBytes) {
		return nil, "", fmt.Errorf("devpatch: patch exceeds %d bytes", maxBytes)
	}
	if found != countRegular(include) {
		return nil, "", fmt.Errorf("devpatch: %d of %d changed files missing from the source archive", countRegular(include)-found, countRegular(include))
	}
	sum := sha256.Sum256(out.Bytes())
	return out.Bytes(), hex.EncodeToString(sum[:]), nil
}

func countRegular(include map[string]bool) int {
	n := 0
	for _, ok := range include {
		if ok {
			n++
		}
	}
	return n
}

// maskMode keeps only whether the file is executable.
func maskMode(mode int64) int64 {
	if mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// ApplyResult summarizes one applied patch.
type ApplyResult struct {
	Written int
	Deleted int
	Bytes   int64
}

// Apply writes a patch archive and its deletions beneath dir. Every file
// operation goes through an os.Root, so neither ".." nor a symlink already in
// the image can redirect a write or delete outside dir. Only regular files
// are written; each one is staged beside its target and renamed into place.
// Deleting a path that is missing or is a directory is a no-op.
func Apply(dir string, archive []byte, deleted []string, maxBytes int64) (ApplyResult, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("devpatch: open %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()
	var result ApplyResult
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return result, fmt.Errorf("devpatch: open patch: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, fmt.Errorf("devpatch: read patch: %w", err)
		}
		name, ok := safeRelative(hdr.Name)
		if !ok || hdr.Typeflag != tar.TypeReg || hdr.Size < 0 {
			return result, fmt.Errorf("%w: %q", ErrUnsafeEntry, hdr.Name)
		}
		if result.Bytes+hdr.Size > maxBytes {
			return result, fmt.Errorf("devpatch: patch exceeds %d bytes", maxBytes)
		}
		if err := writeFile(root, name, tr, hdr.Size, fs.FileMode(maskMode(hdr.Mode))); err != nil {
			return result, err
		}
		result.Written++
		result.Bytes += hdr.Size
	}
	for _, raw := range deleted {
		name, ok := safeRelative(raw)
		if !ok {
			return result, fmt.Errorf("%w: %q", ErrUnsafeEntry, raw)
		}
		info, err := root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && info.IsDir()) {
			continue
		}
		if err != nil {
			return result, fmt.Errorf("devpatch: inspect %s: %w", name, err)
		}
		if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return result, fmt.Errorf("devpatch: delete %s: %w", name, err)
		}
		result.Deleted++
	}
	return result, nil
}

func writeFile(root *os.Root, name string, content io.Reader, size int64, mode fs.FileMode) error {
	if dir := path.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("devpatch: create %s: %w", dir, err)
		}
	}
	staged := path.Join(path.Dir(name), ".gregale-patch-"+path.Base(name))
	f, err := root.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("devpatch: stage %s: %w", name, err)
	}
	_, copyErr := io.CopyN(f, content, size)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = root.Remove(staged)
		return fmt.Errorf("devpatch: write %s: %w", name, errors.Join(copyErr, closeErr))
	}
	if err := root.Chmod(staged, mode); err != nil {
		_ = root.Remove(staged)
		return fmt.Errorf("devpatch: chmod %s: %w", name, err)
	}
	if err := root.Rename(staged, name); err != nil {
		_ = root.Remove(staged)
		return fmt.Errorf("devpatch: replace %s: %w", name, err)
	}
	return nil
}

// safeRelative accepts only clean, relative, non-empty paths that stay
// inside the target directory.
func safeRelative(name string) (string, bool) {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) {
		return "", false
	}
	clean := path.Clean(name)
	if clean != name || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}
