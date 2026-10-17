//go:build linux

package vmmdgrpc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// In the v1 compatibility bridge Bash can spawn body-copy children. Cancelling
// only Bash leaves those children holding guest sockets and stdout pipes.
// Own the entire group so bridge cleanup really precedes permit release.
func configureForwardCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = streamBridgeSysProcAttr()
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
