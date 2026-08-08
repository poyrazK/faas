//go:build linux

// Stream-bridge parent-death signal. Linux is the production target
// (ADR-009 netns invariant + Pdeathsig field on syscall.SysProcAttr
// is only defined for linux). SIGTERM is the same signal vmmd sends
// on graceful shutdown, so a graceful path is unchanged; only the
// SIGKILL escape gets cleaned up. See streamBridgeSpawnReal for the
// full rationale (finding #3 from PR #754's medium code review).

package vmmdgrpc

import "syscall"

func streamBridgeSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
	}
}
