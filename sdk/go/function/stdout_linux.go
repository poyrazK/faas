//go:build linux

package function

import (
	"io"
	"os"
	"syscall"
)

// reserveStdout keeps the original stdout for the protocol and points file
// descriptor 1 at stderr, so every write to stdout — including loggers that
// captured os.Stdout before Serve was called — reaches the log stream instead
// of corrupting a response envelope.
func reserveStdout() io.Writer {
	fd, err := syscall.Dup(1)
	if err != nil {
		return reserveStdoutVar()
	}
	syscall.CloseOnExec(fd)
	if err := syscall.Dup3(2, 1, 0); err != nil {
		_ = syscall.Close(fd)
		return reserveStdoutVar()
	}
	return os.NewFile(uintptr(fd), "faas-protocol")
}

func reserveStdoutVar() io.Writer {
	out := os.Stdout
	os.Stdout = os.Stderr
	return out
}
