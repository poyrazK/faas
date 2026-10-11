//go:build linux

package runtimequalification

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureNativeCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// CommandContext calls Cancel only after Start. Kill the complete owned
	// command group, then Wait joins stdout/stderr before final leakcheck.
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
