// adr: 171 — provider-neutral reserved public IP lease state and deterministic failover selection.
package networkip

import (
	"net/netip"
	"testing"
)

func TestValidateAddress(t *testing.T) {
	tests := []struct {
		name string
		addr string
		ok   bool
	}{
		{name: "public v4", addr: "203.0.113.10", ok: true},
		{name: "public v6", addr: "2001:db8::10", ok: true},
		{name: "private", addr: "10.0.0.10"},
		{name: "loopback", addr: "127.0.0.1"},
		{name: "link local", addr: "169.254.1.1"},
		{name: "multicast", addr: "239.1.1.1"},
		{name: "unspecified", addr: "0.0.0.0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tc.addr)
			if got := ValidateAddress(addr) == nil; got != tc.ok {
				t.Fatalf("ValidateAddress(%s) = %v, want %v", addr, got, tc.ok)
			}
		})
	}
}

func TestValidateTransition(t *testing.T) {
	for _, tc := range []struct {
		from, to Status
		ok       bool
	}{
		{StatusAvailable, StatusPending, true},
		{StatusPending, StatusAssigned, true},
		{StatusAssigned, StatusPending, true},
		{StatusPending, StatusAvailable, true},
		{StatusError, StatusAssigned, false},
		{StatusAvailable, StatusAssigned, false},
	} {
		t.Run(string(tc.from)+"-"+string(tc.to), func(t *testing.T) {
			got := ValidateTransition(tc.from, tc.to) == nil
			if got != tc.ok {
				t.Fatalf("ValidateTransition(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.ok)
			}
		})
	}
}

func TestSelectFailoverNodeDeterministicAndRegionScoped(t *testing.T) {
	got, ok := SelectFailoverNode("node-a", "fra1", []Node{
		{ID: "node-z", Region: "fra1", Active: true},
		{ID: "node-b", Region: "ams1", Active: true},
		{ID: "node-empty-region", Active: true},
		{ID: "node-c", Region: "fra1", Active: false},
		{ID: "node-d", Region: "fra1", Active: true},
		{ID: "node-d", Region: "fra1", Active: true},
	})
	if !ok || got != "node-d" {
		t.Fatalf("SelectFailoverNode = (%q, %v), want (node-d, true)", got, ok)
	}
	if _, ok := SelectFailoverNode("node-a", "fra1", []Node{{ID: "node-b", Region: "ams1", Active: true}}); ok {
		t.Fatal("selected a node outside the reserved IP region")
	}
}
