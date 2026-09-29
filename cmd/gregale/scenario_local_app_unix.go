//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureLocalAppProcess(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func signalLocalAppProcess(command *exec.Cmd, force bool) error {
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	err := syscall.Kill(-command.Process.Pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func localAppProcessGroupAlive(command *exec.Cmd) bool {
	return syscall.Kill(-command.Process.Pid, 0) == nil
}

func testTerminationSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }
