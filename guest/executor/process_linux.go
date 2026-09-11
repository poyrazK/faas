//go:build linux

package executor

import (
	"os/exec"
	"syscall"
)

// configureProcess keeps customer-created descendants in a private process
// group and drops the interpreter to the unprivileged runtime identity. The
// guest init remains root only long enough to bind vsock and launch this
// process; no customer code runs as PID 1.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Credential: &syscall.Credential{
			Uid: 1000,
			Gid: 1000,
		},
	}
}

func terminateProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Kill the whole customer process group. Node's child_process and Python's
	// subprocess can otherwise outlive a timed-out top-level interpreter.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
