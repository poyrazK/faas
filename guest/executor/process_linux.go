//go:build linux

package executor

import (
	"os"
	"os/exec"
	"syscall"
)

// configureProcess keeps customer-created descendants in a private process
// group and drops the interpreter to the unprivileged runtime identity. The
// guest init remains root only long enough to bind vsock and launch this
// process; no customer code runs as PID 1.
func configureProcess(cmd *exec.Cmd) {
	attrs := &syscall.SysProcAttr{Setpgid: true}
	// guest-init is root in the production microVM and can enforce the
	// dedicated runtime uid. Local tests and developer builds often already
	// run as an unprivileged user; asking the kernel to switch identities there
	// would make an otherwise valid interpreter launch fail with EPERM.
	if os.Geteuid() == 0 {
		attrs.Credential = &syscall.Credential{Uid: 1000, Gid: 1000}
	}
	cmd.SysProcAttr = attrs
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
