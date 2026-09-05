//go:build linux

package fcvm

import (
	"errors"
	"os/exec"

	"golang.org/x/sys/unix"
)

func bindFileMount(source, target string) ([]byte, error) {
	return nil, unix.Mount(source, target, "", unix.MS_BIND, "")
}

func makeFileMountReadOnly(target string) ([]byte, error) {
	// Change only the read-only attribute. A legacy bind remount assembled
	// from scratch could accidentally clear inherited nodev/nosuid/noexec.
	err := unix.MountSetattr(unix.AT_FDCWD, target, 0, &unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY})
	if err == nil {
		return nil, nil
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL) {
		// Preserve util-linux's mount-flag handling on older host kernels.
		return exec.Command("mount", "-o", "remount,bind,ro", target).CombinedOutput()
	}
	return nil, err
}
