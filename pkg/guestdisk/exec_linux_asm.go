//go:build linux && (amd64 || arm64)

package guestdisk

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// execWithAddressLimit applies the hard and soft memory cap and immediately
// execs the tool. Assembly keeps Go stack growth, scheduler calls and race or
// atomic coverage instrumentation out of the limit-to-exec transition: the
// helper may already have more address space mapped than the tool is allowed.
// All arguments and all other limits are prepared before this call.
//
//go:noescape
func execWithAddressLimit(path *byte, argv, envv **byte, limit *unix.Rlimit) syscall.Errno
