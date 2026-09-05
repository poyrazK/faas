package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// prepareBuildSourceModes allows the mapped rootless builder to access the
// source without erasing executable bits or chmodding symlink targets.
func prepareBuildSourceModes(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0o666)
		if info.IsDir() {
			mode = 0o777
		} else if info.Mode().IsRegular() {
			mode |= info.Mode().Perm() & 0o111
		} else {
			return fmt.Errorf("unsupported build source type: %s", path)
		}
		return os.Chmod(path, mode)
	})
}
