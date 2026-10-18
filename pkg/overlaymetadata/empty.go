package overlaymetadata

import (
	"errors"
	"io"
	"os"
)

// EnsureEmptyLowerDirectory prepares a platform-owned empty lower. Callers
// keep its parent private until the guest or scanner overlay has been mounted.
func EnsureEmptyLowerDirectory(path string) (retErr error) {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() {
		return errors.Join(ErrInvalid, err)
	}
	dir, err := os.Open(path) //nolint:forbidigo // Platform-owned directory: Lstat rejects symlinks and SameFile fences the opened descriptor and path.
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, dir.Close()) }()
	opened, err := dir.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return errors.Join(ErrInvalid, err)
	}
	entries, err := dir.ReadDir(1)
	if len(entries) != 0 || err != nil && !errors.Is(err, io.EOF) {
		return errors.Join(ErrInvalid, err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		return errors.Join(ErrInvalid, err)
	}
	return nil
}
