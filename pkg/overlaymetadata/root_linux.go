//go:build linux

package overlaymetadata

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

func readNativeRootXattr(root, name string, value []byte) (int, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
	if err != nil {
		return 0, err
	}
	n, readErr := unix.Fgetxattr(fd, name, value)
	closeErr := unix.Close(fd)
	if errors.Is(readErr, syscall.ENODATA) && closeErr == nil {
		return 0, ErrAbsent
	}
	return n, errors.Join(readErr, closeErr)
}
