package builderd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrSourceBoundary identifies a source path or source object that violates
// builderd's local trust boundary. These errors are terminal input/storage
// errors; retrying the same build would not make the source valid.
var ErrSourceBoundary = errors.New("builderd: source boundary violation")

// The largest currently supported source plan is 250 MiB. This fallback keeps
// the storage seam bounded even when a test or an older caller omits a plan
// limit; production calls pass the account's exact plan limit.
const defaultSourceMaxBytes int64 = 250 << 20

func effectiveSourceMaxBytes(maxBytes int64) int64 {
	if maxBytes <= 0 {
		return defaultSourceMaxBytes
	}
	return maxBytes
}

// validatePathWithinRoot applies lexical containment to a path derived from a
// persisted database row. The root is intentionally optional for legacy
// in-process callers; production cmd/builderd always supplies it.
func validatePathWithinRoot(path, root string) error {
	if root == "" {
		return nil
	}
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return fmt.Errorf("%w: source path and spool root must be absolute", ErrSourceBoundary)
	}
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%w: path %q is outside spool root %q", ErrSourceBoundary, path, root)
	}
	return nil
}

// rejectSymlinkComponents closes the directory-symlink variant of a path
// escape. Lstat is used for every existing component so a path cannot enter
// an attacker-controlled directory while it is being materialized.
func rejectSymlinkComponents(path, root string) error {
	if root == "" {
		return nil
	}
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("%w: resolve path components: %w", ErrSourceBoundary, err)
	}
	check := func(candidate string) error {
		info, err := os.Lstat(candidate)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("%w: inspect path component %q: %w", ErrSourceBoundary, candidate, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: path component %q is a symlink", ErrSourceBoundary, candidate)
		}
		return nil
	}
	if err := check(root); err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	current := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		current = filepath.Join(current, part)
		if err := check(current); err != nil {
			return err
		}
	}
	return nil
}

func validateSourcePath(path, root string) error {
	if err := validatePathWithinRoot(path, root); err != nil {
		return err
	}
	return rejectSymlinkComponents(path, root)
}

// validateSourceFile checks the final source object after a spool sync and
// immediately before builderd hands it to framework detection/staging.
func validateSourceFile(path, root string, expectedBytes, maxBytes int64) (int64, error) {
	if err := validateSourcePath(path, root); err != nil {
		return 0, err
	}
	if expectedBytes < 0 {
		return 0, fmt.Errorf("%w: declared source size %d is negative", ErrSourceBoundary, expectedBytes)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return 0, fmt.Errorf("source file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("%w: source path %q is a symlink", ErrSourceBoundary, path)
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("%w: source path %q is not a regular file", ErrSourceBoundary, path)
	}
	if info.Size() == 0 {
		return 0, fmt.Errorf("%w: source archive %q is empty", ErrSourceBoundary, path)
	}
	if expectedBytes > 0 && info.Size() != expectedBytes {
		return 0, fmt.Errorf("%w: source size mismatch: expected %d bytes, got %d", ErrSourceBoundary, expectedBytes, info.Size())
	}
	maxBytes = effectiveSourceMaxBytes(maxBytes)
	if info.Size() > maxBytes {
		return 0, fmt.Errorf("%w: source size %d exceeds %d-byte cap", ErrSourceBoundary, info.Size(), maxBytes)
	}
	return info.Size(), nil
}

// copySourceBounded copies at most maxBytes bytes and reads one additional
// byte only when the budget is full, so an oversized object is detected
// without writing beyond the configured source budget.
func copySourceBounded(dst io.Writer, src io.Reader, expectedBytes, maxBytes int64) (int64, error) {
	if expectedBytes < 0 {
		return 0, fmt.Errorf("%w: declared source size %d is negative", ErrSourceBoundary, expectedBytes)
	}
	maxBytes = effectiveSourceMaxBytes(maxBytes)
	readLimit := maxBytes
	if expectedBytes > 0 && expectedBytes < readLimit {
		readLimit = expectedBytes
	}
	n, err := io.Copy(dst, io.LimitReader(src, readLimit))
	if err != nil {
		return n, err
	}
	if n > readLimit {
		if expectedBytes > 0 && expectedBytes < maxBytes {
			return n, fmt.Errorf("%w: source stream exceeds declared size %d", ErrSourceBoundary, expectedBytes)
		}
		return n, fmt.Errorf("%w: source stream exceeds %d-byte cap", ErrSourceBoundary, maxBytes)
	}
	if n == readLimit {
		var extra [1]byte
		extraN, extraErr := io.ReadFull(src, extra[:])
		if extraN > 0 {
			if expectedBytes > 0 && expectedBytes < maxBytes {
				return n, fmt.Errorf("%w: source stream exceeds declared size %d", ErrSourceBoundary, expectedBytes)
			}
			return n, fmt.Errorf("%w: source stream exceeds %d-byte cap", ErrSourceBoundary, maxBytes)
		}
		if extraErr != nil && !errors.Is(extraErr, io.EOF) {
			return n, extraErr
		}
	}
	if expectedBytes > 0 && n != expectedBytes {
		return n, fmt.Errorf("%w: source size mismatch: expected %d bytes, got %d", ErrSourceBoundary, expectedBytes, n)
	}
	if n == 0 {
		return n, fmt.Errorf("%w: source stream is empty", ErrSourceBoundary)
	}
	return n, nil
}
