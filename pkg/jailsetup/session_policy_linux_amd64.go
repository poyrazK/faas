//go:build linux && amd64

package jailsetup

// adr: 783
import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Preserve the launch session through fork/exec. This is an affiliation rule
// for trusted host utilities, not a general sandbox for arbitrary root code.
func hostHelperSessionFilter() []unix.SockFilter {
	return []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4}, // seccomp_data.arch
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 1, K: unix.AUDIT_ARCH_X86_64},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0}, // seccomp_data.nr
		// Reject x32, negative numbers and historical compat aliases. Host
		// utilities use the native x86_64 ABI; no compat bypass is permitted.
		{Code: unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K, Jt: 1, K: 512},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 1, K: unix.SYS_SETSID},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	}
}

func installHostHelperSessionPolicy() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("native helper: set no-new-privileges: %w", err)
	}
	filters := hostHelperSessionFilter()
	program := unix.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
	// #nosec G103 -- seccomp synchronously copies this owned SockFprog and its eight filters; the inline syscall conversion and KeepAlive retain them until return.
	result, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&program)))
	runtime.KeepAlive(filters)
	if errno != 0 {
		return fmt.Errorf("native helper: install inherited session policy: %w", errno)
	}
	if result != 0 {
		return errors.New("native helper: session policy did not synchronize every thread")
	}
	return nil
}

func acknowledgeHostHelperSessionPolicy(fd int) error {
	writer := os.NewFile(uintptr(fd), "native-helper-session-ready")
	if writer == nil {
		return errors.New("native helper: session policy acknowledgement missing")
	}
	defer func() { _ = writer.Close() }()
	if err := installHostHelperSessionPolicy(); err != nil {
		return err
	}
	_, err := writer.Write([]byte{HostHelperSessionPolicyVersion})
	return errors.Join(err, writer.Close())
}
