package rootfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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

// chmodImageDirectory refuses to follow a symlink at path. Image symlink
// targets are guest-side strings that can name host directories, and imaged
// holds CAP_FOWNER, so following one would change a host directory's mode.
func chmodImageDirectory(path string, mode os.FileMode) error {
	dir, err := os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	return errors.Join(dir.Chmod(mode.Perm()), dir.Close())
}
