//go:build linux

package imaged

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// mountOverlaySyscall is the linux implementation of mountOverlayFn.
// It invokes unix.Mount(2) directly so the daemon's ambient
// CAP_SYS_ADMIN (AmbientCapabilities=cap_sys_admin in the
// faas-imaged.service unit) is honored end-to-end without going
// through the setuid-root /bin/mount binary, which drops caps when
// invoked under NoNewPrivileges=yes. Direct mount(2) accepts the
// cap passed by the kernel because no privilege-changing exec
// happens.
//
// The opts string is the literal "lowerdir=...,upperdir=...,workdir=..."
// overlay mount data — passed as the data string so the kernel
// parses it (vs the cgo-ish unsafe.Pointer path on darwin).
func mountOverlaySyscall(merged, lowerdir, upperdir, workdir string) error {
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerdir, upperdir, workdir)
	if err := unix.Mount("overlay", merged, "overlay", uintptr(0), opts); err != nil {
		return fmt.Errorf("mount overlay %s -o %s: %w", merged, opts, err)
	}
	return nil
}

// umountOverlaySyscall mirrors mountOverlaySyscall for teardown.
// EINVAL/ENOENT (target not mounted) is absorbed so the caller can
// defer it safely.
func umountOverlaySyscall(merged string) error {
	err := unix.Unmount(merged, 0)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOENT) {
		return nil
	}
	return fmt.Errorf("umount overlay %s: %w", merged, err)
}
