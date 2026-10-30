//go:build linux

package fcvm

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// adoptedGuestRoot is a process's root directory; tests stand in a fake jail.
var adoptedGuestRoot = func(pid int) string { return "/proc/" + strconv.Itoa(pid) + "/root" }

// openAdoptedBindTarget opens the file an adopted guest's jail has bound at
// rel, seen through its Firecracker's root, when that file is still want.
// The cache can re-stage a source path under a live guest (production
// rc.306: a release's base pre-stage rewrote a shared cache file); the guest
// keeps the recorded inode through its bind mount, and this reaches it there.
// Resolution stays beneath the jail root and follows no link, and the name is
// opened O_PATH, for nothing else, until its identity is proven: a name the
// guest controls cannot make vmmd open a FIFO, a device or another file.
func openAdoptedBindTarget(pid int, rel string, want resourceFileIdentity) (*os.File, error) {
	root, err := unix.Open(adoptedGuestRoot(pid), unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open guest root: %w", err)
	}
	defer func() { _ = unix.Close(root) }()
	fd, err := unix.Openat2(root, rel, &unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return nil, fmt.Errorf("open bind target %s in the guest's root: %w", rel, err)
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || (resourceFileIdentity{Device: st.Dev, Inode: st.Ino}) != want {
		return nil, errors.New("the guest's bind target is not the recorded file")
	}
	f, err := os.OpenFile("/proc/self/fd/"+strconv.Itoa(fd), os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("reopen bind target %s: %w", rel, err)
	}
	if info, err := f.Stat(); err != nil {
		_ = f.Close()
		return nil, err
	} else if id, err := resourceFileID(info); err != nil || id != want {
		_ = f.Close()
		return nil, errors.New("the guest's bind target changed while it was opened")
	}
	return f, nil
}
