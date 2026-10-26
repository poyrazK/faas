//go:build linux

package wire

import (
	"io"
	"log/slog"
	"net"
)

// PeerCredListener wraps ln with an explicit allowed-uid set, so a test can
// exercise the refusal path without a second user.
func PeerCredListener(ln net.Listener, allowed map[uint32]bool) net.Listener {
	return &peerCredListener{Listener: ln, allowed: allowed, log: slog.New(slog.NewTextHandler(io.Discard, nil)), daemon: "test"}
}
