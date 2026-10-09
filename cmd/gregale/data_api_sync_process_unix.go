//go:build unix

package main

import "os/exec"

func configureDataAPISyncProcess(command *exec.Cmd) {
	_ = configureLocalAppProcess(command)
	command.Cancel = func() error { return signalLocalAppProcess(command, true) }
}

func cleanupDataAPISyncProcess(command *exec.Cmd) {
	if command.Process != nil {
		_ = signalLocalAppProcess(command, true)
	}
}
