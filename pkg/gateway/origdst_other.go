//go:build !linux

package gateway

import (
	"errors"
	"net"
	"net/netip"
)

// TCPOriginalDestination is Linux-only: it reads conntrack through
// SO_ORIGINAL_DST (ADR-530). Other platforms never host the service TCP
// proxy, so the lookup fails closed.
func TCPOriginalDestination(net.Conn) (netip.AddrPort, error) {
	return netip.AddrPort{}, errors.New("original destination is only available on Linux")
}
