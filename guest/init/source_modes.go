package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// buildSourceStableTime replaces the Unix-epoch modification time the CLI
// packer stamps on every archive entry (cmd/gregale packEpoch). Build tools
// that copy "if the source is newer" compare the source's mtime with a missing
// destination's zero time, so a file dated 0 is never copied: Maven's
// resources plugin left an empty data/ directory in the retail-store UI JAR
// (Could not find tags.json in classpath, rc.381). 1980-01-01, the ZIP epoch,
// keeps the build reproducible and is later than any missing file.
var buildSourceStableTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// prepareBuildSourceModes allows the mapped rootless builder to access the
// source without erasing executable bits or chmodding symlink targets, and
// moves epoch-dated entries to buildSourceStableTime.
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
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
		if info.ModTime().Unix() > 0 {
			return nil
		}
		return os.Chtimes(path, buildSourceStableTime, buildSourceStableTime)
	})
}
