//go:build linux

package gateway

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"

	"golang.org/x/sys/unix"
)

// TCPOriginalDestination returns the pre-DNAT destination of an accepted
// connection (ADR-568). The host prerouting chain DNATs a guest's
// connection to a private service address onto the service TCP proxy;
// conntrack in this namespace still records the address the guest dialed.
// The lookup needs no capability, unlike an IP_TRANSPARENT listener.
func TCPOriginalDestination(conn net.Conn) (netip.AddrPort, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return netip.AddrPort{}, errors.New("original destination: connection exposes no socket")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("original destination: %w", err)
	}
	var (
		dst     netip.AddrPort
		sockErr error
	)
	if err := raw.Control(func(fd uintptr) {
		// SO_ORIGINAL_DST fills a struct sockaddr_in; IPv6Mreq is the
		// 16-byte carrier x/sys exposes for it: family(2) port(2, big
		// endian) addr(4) zero(8).
		mreq, err := unix.GetsockoptIPv6Mreq(int(fd), unix.SOL_IP, unix.SO_ORIGINAL_DST)
		if err != nil {
			sockErr = err
			return
		}
		raw := mreq.Multiaddr
		dst = netip.AddrPortFrom(netip.AddrFrom4([4]byte(raw[4:8])), binary.BigEndian.Uint16(raw[2:4]))
	}); err != nil {
		return netip.AddrPort{}, fmt.Errorf("original destination: %w", err)
	}
	if sockErr != nil {
		return netip.AddrPort{}, fmt.Errorf("original destination: getsockopt SO_ORIGINAL_DST: %w", sockErr)
	}
	return dst, nil
}
