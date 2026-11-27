package e2etest

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func daemonProcessExited(process *os.Process) bool {
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return true
	}
	var info unix.Siginfo
	// WNOWAIT preserves the child's exit status for harness teardown.
	err := unix.Waitid(unix.P_PID, process.Pid, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil)
	return err == nil && info.Signo != 0
}
