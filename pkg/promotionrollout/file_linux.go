//go:build linux

package promotionrollout

import (
	"os"

	"golang.org/x/sys/unix"
)

// A replacement with a FIFO or symlink must not block an admission worker.
func openReadOnly(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
