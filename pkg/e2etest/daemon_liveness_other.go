//go:build !linux

package e2etest

import (
	"os"
	"syscall"
)

func daemonProcessExited(process *os.Process) bool {
	return process.Signal(syscall.Signal(0)) != nil
}
