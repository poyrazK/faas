package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// buildSourceStableTime replaces the Unix-epoch modification time the CLI
// packer stamps on every archive entry (cmd/gregale packEpoch). Build tools
// that copy "if the source is newer" compare the source's mtime with a missing
// destination's zero time, so a file dated 0 is never copied: Maven's
// resources plugin left an empty data/ directory in the retail-store UI JAR
// (Could not find tags.json in classpath, rc.381). The date is one day past
// the ZIP epoch so it is 1980 or later in every time zone, which ZIP-writing
// build steps require, and it keeps the build reproducible.
var buildSourceStableTime = time.Date(1980, 1, 2, 0, 0, 0, 0, time.UTC)

// prepareBuildSourceModes allows the mapped rootless builder to access the
// source without erasing executable bits or chmodding symlink targets, and
// moves epoch-dated entries, symlinks included, to buildSourceStableTime.
func prepareBuildSourceModes(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return stabilizeSourceTime(path, info, true)
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
		return stabilizeSourceTime(path, info, false)
	})
}

// stabilizeSourceTime moves an entry dated at or before the Unix epoch to
// buildSourceStableTime. A symlink's own time changes, never its target's.
func stabilizeSourceTime(path string, info fs.FileInfo, symlink bool) error {
	if info.ModTime().Unix() > 0 {
		return nil
	}
	var err error
	if symlink {
		tv := unix.NsecToTimeval(buildSourceStableTime.UnixNano())
		err = unix.Lutimes(path, []unix.Timeval{tv, tv})
	} else {
		err = os.Chtimes(path, buildSourceStableTime, buildSourceStableTime)
	}
	if err != nil {
		return fmt.Errorf("set stable source time on %s: %w", path, err)
	}
	return nil
}
