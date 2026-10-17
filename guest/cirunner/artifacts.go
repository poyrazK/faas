package cirunner

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// Use the workspace descriptor and no-follow opens for every component.
// os.Root alone follows contained links even when OpenFile gets O_NOFOLLOW.
// A nonblocking leaf open prevents a replaced FIFO from stalling finalization.
func openArtifact(root *os.Root, name string) (*os.File, error) {
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, errTransfer
		}
	}
	directory, err := root.Open(".")
	if err != nil {
		return nil, errTransfer
	}
	defer func() { _ = directory.Close() }()
	for _, part := range parts[:len(parts)-1] {
		fd, err := unix.Openat(int(directory.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, errTransfer
		}
		_ = directory.Close()
		directory = os.NewFile(uintptr(fd), part)
	}
	fd, err := unix.Openat(int(directory.Fd()), parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errTransfer
	}
	file := os.NewFile(uintptr(fd), name)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errTransfer
	}
	return file, nil
}
