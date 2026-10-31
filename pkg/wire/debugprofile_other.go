//go:build !linux

package wire

import (
	"log/slog"
	"net"
)

// listenDebugProfile is Linux-only: the endpoint relies on abstract unix
// sockets and SO_PEERCRED (ADR-20261023200002).
func listenDebugProfile(string, *slog.Logger) (net.Listener, string, error) {
	return nil, "", errDebugProfileUnsupported
}
