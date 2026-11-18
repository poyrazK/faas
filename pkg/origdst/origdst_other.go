//go:build !linux

package origdst

import (
	"errors"
	"net"
	"net/netip"
)

// TCP is Linux-only: it reads conntrack through
// SO_ORIGINAL_DST (ADR-576). Other platforms never host the service TCP
// proxy, so the lookup fails closed.
func TCP(net.Conn) (netip.AddrPort, error) {
	return netip.AddrPort{}, errors.New("original destination is only available on Linux")
}
