package main

// adr: 20261109130000 — guest-init resets off-guest TCP connections before
// vmmd destroys the guest.

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func TestEncodeInetDiagReqLayout(t *testing.T) {
	var id [inetDiagSockIDLen]byte
	for i := range id {
		id[i] = byte(i + 1)
	}
	b := encodeInetDiagReq(sockDestroy, 0x5, 9, 10, releaseStates, &id)
	if len(b) != 16+56 {
		t.Fatalf("request length = %d, want 72", len(b))
	}
	if got := binary.NativeEndian.Uint32(b[0:4]); got != 72 {
		t.Fatalf("nlmsg_len = %d", got)
	}
	if binary.NativeEndian.Uint16(b[4:6]) != sockDestroy || binary.NativeEndian.Uint16(b[6:8]) != 0x5 || binary.NativeEndian.Uint32(b[8:12]) != 9 {
		t.Fatalf("header = %x", b[:16])
	}
	req := b[16:]
	if req[0] != 10 || req[1] != ipprotoTCP || req[2] != 0 || req[3] != 0 {
		t.Fatalf("family/protocol = %x", req[:4])
	}
	if binary.NativeEndian.Uint32(req[4:8]) != releaseStates {
		t.Fatal("states not encoded")
	}
	if string(req[8:]) != string(id[:]) {
		t.Fatal("socket id not copied verbatim")
	}
	dump := encodeInetDiagReq(sockDiagByFamily, 0x301, 1, 2, releaseStates, nil)
	for _, v := range dump[16+8:] {
		if v != 0 {
			t.Fatal("dump request must carry a zero socket id")
		}
	}
}

func TestReleaseStatesSkipListenAndTimeWait(t *testing.T) {
	const listen, timeWait, closed = 10, 6, 7
	for _, s := range []uint{listen, timeWait, closed} {
		if releaseStates&(1<<s) != 0 {
			t.Fatalf("state %d selected", s)
		}
	}
	for _, s := range []uint{tcpEstablished, tcpSynSent, tcpCloseWait, tcpFinWait1} {
		if releaseStates&(1<<s) == 0 {
			t.Fatalf("state %d not selected", s)
		}
	}
}

func inetDiagMsg(family uint8, dst []byte) []byte {
	b := make([]byte, inetDiagMsgLen)
	b[0], b[1] = family, tcpEstablished
	copy(b[inetDiagMsgIDStart+20:], dst)
	return b
}

func TestParseInetDiagMsgPeers(t *testing.T) {
	for name, tc := range map[string]struct {
		family     uint8
		dst        []byte
		peer       string
		releasable bool
	}{
		"ipv4 database":      {2, []byte{10, 20, 0, 5}, "10.20.0.5", true},
		"ipv4 loopback":      {2, []byte{127, 0, 0, 1}, "127.0.0.1", false},
		"ipv6 peer":          {10, netip.MustParseAddr("2001:db8::7").AsSlice(), "2001:db8::7", true},
		"ipv6 loopback":      {10, netip.IPv6Loopback().AsSlice(), "::1", false},
		"v4-mapped loopback": {10, netip.MustParseAddr("::ffff:127.0.0.1").AsSlice(), "127.0.0.1", false},
		"v4-mapped peer":     {10, netip.MustParseAddr("::ffff:192.0.2.9").AsSlice(), "192.0.2.9", true},
	} {
		s, err := parseInetDiagMsg(inetDiagMsg(tc.family, tc.dst))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s.peer.String() != tc.peer || s.releasable() != tc.releasable {
			t.Errorf("%s: peer %s releasable %v, want %s %v", name, s.peer, s.releasable(), tc.peer, tc.releasable)
		}
	}
	if _, err := parseInetDiagMsg(make([]byte, inetDiagMsgLen-1)); err == nil {
		t.Fatal("short message parsed")
	}
	if _, err := parseInetDiagMsg(inetDiagMsg(1, nil)); err == nil {
		t.Fatal("unexpected family parsed")
	}
}
