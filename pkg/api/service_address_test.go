// adr: 576

package api

import (
	"net/netip"
	"testing"
)

func TestServiceAddressForIndex(t *testing.T) {
	tests := []struct {
		name  string
		index int
		want  string
		ok    bool
	}{
		{name: "first", index: 1, want: "198.19.0.1", ok: true},
		{name: "octet carry", index: 256, want: "198.19.1.0", ok: true},
		{name: "last", index: ServiceAddressIndexMax, want: "198.19.255.254", ok: true},
		{name: "network address", index: 0},
		{name: "broadcast address", index: ServiceAddressIndexMax + 1},
		{name: "negative", index: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ServiceAddressForIndex(tt.index)
			if ok != tt.ok {
				t.Fatalf("ServiceAddressForIndex(%d) ok = %v, want %v", tt.index, ok, tt.ok)
			}
			if !tt.ok {
				if got.IsValid() {
					t.Fatalf("ServiceAddressForIndex(%d) = %s, want invalid address", tt.index, got)
				}
				return
			}
			if got.String() != tt.want {
				t.Fatalf("ServiceAddressForIndex(%d) = %s, want %s", tt.index, got, tt.want)
			}
		})
	}
}

func TestServiceAddressIndexOf(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want int
		ok   bool
	}{
		{name: "first", addr: "198.19.0.1", want: 1, ok: true},
		{name: "last", addr: "198.19.255.254", want: ServiceAddressIndexMax, ok: true},
		{name: "ipv4-mapped ipv6", addr: "::ffff:198.19.0.7", want: 7, ok: true},
		{name: "network address", addr: "198.19.0.0"},
		{name: "broadcast address", addr: "198.19.255.255"},
		{name: "sibling benchmarking block", addr: "198.18.0.1"},
		{name: "tenant bridge", addr: "10.100.0.1"},
		{name: "ipv6", addr: "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ServiceAddressIndexOf(netip.MustParseAddr(tt.addr))
			if ok != tt.ok || got != tt.want {
				t.Fatalf("ServiceAddressIndexOf(%s) = (%d, %v), want (%d, %v)", tt.addr, got, ok, tt.want, tt.ok)
			}
		})
	}
	if _, ok := ServiceAddressIndexOf(netip.Addr{}); ok {
		t.Fatal("ServiceAddressIndexOf(zero Addr) ok = true, want false")
	}
}

// Every allocatable index must map to a distinct address that maps back to
// the same index; DNS and the TCP proxy depend on the two directions agreeing.
func TestServiceAddressRoundTripCoversBlock(t *testing.T) {
	seen := make(map[netip.Addr]struct{}, ServiceAddressIndexMax)
	for index := ServiceAddressIndexMin; index <= ServiceAddressIndexMax; index++ {
		addr, ok := ServiceAddressForIndex(index)
		if !ok {
			t.Fatalf("ServiceAddressForIndex(%d) ok = false", index)
		}
		if !ServiceAddressCIDR().Contains(addr) {
			t.Fatalf("index %d address %s is outside %s", index, addr, ServiceAddressCIDR())
		}
		if _, dup := seen[addr]; dup {
			t.Fatalf("index %d reuses address %s", index, addr)
		}
		seen[addr] = struct{}{}
		back, ok := ServiceAddressIndexOf(addr)
		if !ok || back != index {
			t.Fatalf("ServiceAddressIndexOf(%s) = (%d, %v), want (%d, true)", addr, back, ok, index)
		}
	}
}

// The service block must never overlap a network the platform routes for
// other purposes; an overlap would let the host DNAT capture that traffic.
func TestServiceAddressCIDRDisjointFromPlatformNetworks(t *testing.T) {
	block := ServiceAddressCIDR()
	if !block.Addr().Is4() || block.Bits() != 16 {
		t.Fatalf("ServiceAddressCIDR() = %s, want an IPv4 /16", block)
	}
	if block != block.Masked() {
		t.Fatalf("ServiceAddressCIDR() = %s is not a masked prefix", block)
	}
	for _, other := range []netip.Prefix{
		DefaultHostBridgeCIDR(),
		DefaultOverlayCIDR(),
		netip.MustParsePrefix("10.200.0.0/16"), // per-VM static-egress pool (pkg/fcvm/alloc.go)
		netip.MustParsePrefix("10.0.0.0/30"),   // identical inner guest world (ADR-009)
	} {
		if block.Overlaps(other) {
			t.Errorf("ServiceAddressCIDR() %s overlaps platform network %s", block, other)
		}
	}
	for _, denied := range StaticEgressIPDenyCIDRs() {
		if block.Overlaps(denied) {
			t.Errorf("ServiceAddressCIDR() %s overlaps tenant deny entry %s; the netns admission would have to outrank it", block, denied)
		}
	}
}

func TestServiceTCPProxyPortIsReserved(t *testing.T) {
	for _, port := range []int{ServiceBindingPort, ServiceBindingLegacyPort, 443, 53} {
		if ServiceTCPProxyPort == port {
			t.Fatalf("ServiceTCPProxyPort %d collides with an existing tenant-bridge listener", port)
		}
	}
	if ServiceTCPProxyPort < 1024 {
		t.Fatalf("ServiceTCPProxyPort %d is privileged; the TCP proxy must not depend on CAP_NET_BIND_SERVICE", ServiceTCPProxyPort)
	}
}
