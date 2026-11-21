package gateway

import (
	"net"
	"net/netip"

	"github.com/onebox-faas/faas/pkg/origdst"
)

// TCPOriginalDestination returns the pre-DNAT destination of an accepted
// connection (ADR-576); see origdst.TCP.
func TCPOriginalDestination(conn net.Conn) (netip.AddrPort, error) { return origdst.TCP(conn) }
