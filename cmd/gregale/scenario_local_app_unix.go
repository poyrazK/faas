//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
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

// testTerminationSignals cancel a run so its cleanup releases ephemeral test
// apps. SIGHUP (closed terminal) would otherwise kill the CLI with no cleanup
// and leave them until the preview TTL (ChronoLake finding 16).
func testTerminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
}

// surviveBrokenOutput keeps a closed stdout or stderr (SIGPIPE, for example
// `gregale test ... | head`) from killing the CLI before its cleanup runs:
// writes then fail with EPIPE instead. It does not cancel the run, because
// chaos faults deliberately break the run's own connections. A handler,
// unlike an ignored disposition, is not inherited by child processes.
func surviveBrokenOutput() (stop func()) {
	pipe := make(chan os.Signal, 1)
	signal.Notify(pipe, syscall.SIGPIPE)
	return func() { signal.Stop(pipe) }
}
