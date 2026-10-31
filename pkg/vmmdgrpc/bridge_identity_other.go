//go:build !linux

package vmmdgrpc

import (
	"errors"
	"syscall"
)

// Unprivileged bridges run only on Linux.
func unixPeerUID(syscall.Conn) (int, error) {
	return 0, errors.New("bridge peer credentials are supported only on linux")
}
