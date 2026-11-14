//go:build linux && !amd64 && !arm64

package guestdisk

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Other Linux architectures retain the raw syscall path. The supported
// compute architectures use an assembly transition, including under coverage.
//
//go:nosplit
//go:norace
func execWithAddressLimit(path *byte, argv, envv **byte, limit *unix.Rlimit) syscall.Errno {
	_, _, errno := syscall.RawSyscall6(syscall.SYS_PRLIMIT64, 0, unix.RLIMIT_AS,
		uintptr(unsafe.Pointer(limit)), 0, 0, 0) //nolint:gosec // G103: raw prlimit64 keeps the limit-to-exec transition allocation-free.
	if errno != 0 {
		return errno
	}
	_, _, errno = syscall.RawSyscall(syscall.SYS_EXECVE,
		uintptr(unsafe.Pointer(path)), //nolint:gosec // G103: raw execve, so nothing allocates under the limits.
		uintptr(unsafe.Pointer(argv)), //nolint:gosec // G103: see above.
		uintptr(unsafe.Pointer(envv))) //nolint:gosec // G103: see above.
	return errno
}
