//go:build !linux

package vmmdgrpc

import "os/exec"

// Production namespace forwarding runs only on Linux. Portable tests keep
// ordinary CommandContext cancellation for their fixture processes.
func configureForwardCommand(cmd *exec.Cmd) { cmd.SysProcAttr = streamBridgeSysProcAttr() }
