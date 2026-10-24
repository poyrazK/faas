//go:build !linux

package vmmdgrpc

import (
	"errors"
	"net"
)

// Unprivileged bridges run only on Linux.
func unixPeerUID(*net.UnixConn) (int, error) {
	return 0, errors.New("bridge peer credentials are supported only on linux")
}
