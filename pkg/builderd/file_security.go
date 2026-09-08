package builderd

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// openNoFollow makes the final path component non-followable. Callers still
// validate parent components when a configured root is available; O_NOFOLLOW
// closes the final-component replacement race between Lstat and open.
func openNoFollow(path string, flags int, perm os.FileMode) (*os.File, error) {
	fd, err := unix.Open(path, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(perm.Perm()))
	if err != nil {
		return nil, fmt.Errorf("open %q without following symlinks: %w", path, err)
	}
	f := os.NewFile(uintptr(fd), path)
	if f == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open %q without following symlinks: invalid file descriptor", path)
	}
	return f, nil
}
