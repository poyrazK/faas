package rootfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// mkdirImageDirectories preserves the requested mode on newly created image
// directories without changing a private host staging root or existing OCI
// directory metadata. Paths have already passed the staging containment checks.
func mkdirImageDirectories(path string, mode os.FileMode) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("image parent %q is not a directory", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if parent != path {
		if err := mkdirImageDirectories(parent, mode); err != nil {
			return err
		}
	}
	if err := os.Mkdir(path, mode); err != nil {
		if errors.Is(err, os.ErrExist) {
			info, statErr := os.Stat(path)
			if statErr != nil {
				return statErr
			}
			if !info.IsDir() {
				return fmt.Errorf("image parent %q is not a directory", path)
			}
			return nil
		}
		return err
	}
	return chmodImageDirectory(path, mode)
}

func chmodImageDirectory(path string, mode os.FileMode) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Chmod(mode.Perm()), dir.Close())
}
