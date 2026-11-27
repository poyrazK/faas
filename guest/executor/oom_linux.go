package executor

import (
	"os"
	"syscall"
)

func readGuestOOMKills() (uint64, bool) {
	f, err := os.Open("/proc/vmstat") //nolint:forbidigo // Fixed guest-kernel counter; no caller path or host data.
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	return guestOOMKills(f)
}

func processKilled(state *os.ProcessState) bool {
	if state == nil {
		return false
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}
