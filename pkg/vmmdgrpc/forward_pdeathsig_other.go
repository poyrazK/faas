//go:build !linux

// Stream-bridge parent-death signal — non-linux platforms.
//
// The Pdeathsig field on syscall.SysProcAttr is linux-only; the v2
// spawn itself is also gated on `ip netns exec` which is linux-only.
// The macOS build compiles this file to satisfy the symbol but
// returns nil; macOS unit tests run with a fake bridge spawned via
// exec.CommandContext directly and don't need parent-death semantics
// (the parent test process is short-lived).

package vmmdgrpc

import "syscall"

func streamBridgeSysProcAttr() *syscall.SysProcAttr {
	return nil
}
