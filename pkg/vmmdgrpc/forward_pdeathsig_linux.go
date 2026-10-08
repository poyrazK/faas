//go:build linux

// Stream-bridge parent-death signal. Linux is the production target
// (ADR-009 netns invariant + Pdeathsig field on syscall.SysProcAttr
// is only defined for linux). A dead permit owner cannot allow an old
// bridge to forward during its successor's startup. Parent death therefore
// kills the bridge immediately; normal shutdown still drains with SIGTERM.
// See streamBridgeSpawnReal for the
// full rationale (finding #3 from PR #754's medium code review).

package vmmdgrpc

import "syscall"

func streamBridgeSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
		Setpgid:   true,
	}
}
