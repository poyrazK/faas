//go:build linux || darwin

package runrelease

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Pin the containing directory and refuse symlink/nonregular final inputs.
// NONBLOCK ensures a FIFO cannot hang a root preflight before fstat rejects it.
func openInput(path string) (*os.File, error) {
	if !cleanPath(path) {
		return nil, errors.New("release input requires a canonical absolute path")
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(filepath.Base(path), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		_ = f.Close()
		return nil, errors.New("release input must be a regular file without group/world write")
	}
	return f, nil
}
