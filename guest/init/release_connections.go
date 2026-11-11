package main

import (
	"encoding/binary"
	"errors"
	"net/netip"
)

// Connection release before destroy (ADR-20261109130000). vmmd destroys a
// guest without the guest sending anything on its sockets, so every database,
// cache or upstream the app held a connection to keeps that session open
// until its own keepalive fails. guest-init resets every TCP connection whose
// peer is off the guest (SOCK_DESTROY sends an RST) when vmmd asks, right
// before the destroy.
//
// This file holds the sock_diag wire encoding, which is pure and tested on
// every platform; release_connections_linux.go does the netlink I/O.

const (
	sockDiagByFamily = 20 // SOCK_DIAG_BY_FAMILY
	sockDestroy      = 21 // SOCK_DESTROY
	ipprotoTCP       = 6

	nlmsgHdrLen        = 16
	inetDiagSockIDLen  = 48
	inetDiagReqV2Len   = 8 + inetDiagSockIDLen // family, protocol, ext, pad, states, id
	inetDiagMsgLen     = 4 + inetDiagSockIDLen + 20
	inetDiagMsgIDStart = 4
)

// TCP states, as include/net/tcp_states.h numbers them.
const (
	tcpEstablished = 1
	tcpSynSent     = 2
	tcpSynRecv     = 3
	tcpFinWait1    = 4
	tcpFinWait2    = 5
	tcpCloseWait   = 8
	tcpLastAck     = 9
	tcpClosing     = 11
)

// releaseStates selects every connection that still has a peer holding state:
// not LISTEN (no peer), TIME_WAIT or CLOSE (the peer is already done).
const releaseStates uint32 = 1<<tcpEstablished | 1<<tcpSynSent | 1<<tcpSynRecv |
	1<<tcpFinWait1 | 1<<tcpFinWait2 | 1<<tcpCloseWait | 1<<tcpLastAck | 1<<tcpClosing

// diagSocket is one socket from a sock_diag dump: its family, its raw
// inet_diag_sockid (ports, addresses, interface and cookie) and its peer.
type diagSocket struct {
	family uint8
	id     [inetDiagSockIDLen]byte
	peer   netip.Addr
}

// encodeInetDiagReq builds a sock_diag netlink request: a dump of TCP sockets
// of one family in releaseStates, or, with id set, a SOCK_DESTROY of one.
func encodeInetDiagReq(msgType, flags uint16, seq uint32, family uint8, states uint32, id *[inetDiagSockIDLen]byte) []byte {
	b := make([]byte, nlmsgHdrLen+inetDiagReqV2Len)
	binary.NativeEndian.PutUint32(b[0:4], uint32(len(b)))
	binary.NativeEndian.PutUint16(b[4:6], msgType)
	binary.NativeEndian.PutUint16(b[6:8], flags)
	binary.NativeEndian.PutUint32(b[8:12], seq)
	req := b[nlmsgHdrLen:]
	req[0] = family
	req[1] = ipprotoTCP
	binary.NativeEndian.PutUint32(req[4:8], states)
	if id != nil {
		copy(req[8:], id[:])
	}
	return b
}

// parseInetDiagMsg reads one inet_diag_msg payload. The sockid's addresses are
// in network order; an IPv4 address fills the first four bytes.
func parseInetDiagMsg(payload []byte) (diagSocket, error) {
	if len(payload) < inetDiagMsgLen {
		return diagSocket{}, errors.New("inet_diag_msg too short")
	}
	s := diagSocket{family: payload[0]}
	copy(s.id[:], payload[inetDiagMsgIDStart:inetDiagMsgIDStart+inetDiagSockIDLen])
	dst := s.id[20:36]
	switch s.family {
	case 2: // AF_INET
		s.peer = netip.AddrFrom4([4]byte(dst[:4]))
	case 10: // AF_INET6
		s.peer = netip.AddrFrom16([16]byte(dst)).Unmap()
	default:
		return diagSocket{}, errors.New("inet_diag_msg: unexpected family")
	}
	return s, nil
}

// releasable reports whether a socket's peer is off the guest. Loopback peers
// are the app talking to itself or a sidecar, which die with the VM.
func (s diagSocket) releasable() bool {
	return s.peer.IsValid() && !s.peer.IsLoopback() && !s.peer.IsUnspecified()
}
