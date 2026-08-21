// Unit tests for the ADR-119 deny-set helper
// (pkg/netns/static_egress_ip_denylist.go). These are the
// single-source-of-truth pins: every layer that consumes
// ValidateStaticEgressIP (apid, fcvm, vmmd bundle, metal test)
// must agree. If a future maintainer adds e.g. 198.18.0.0/15 to
// the deny set, the new entry shows up here and every consumer
// picks it up via the shared slice.
package api

import (
	"net/netip"
	"testing"
)

func TestValidateStaticEgressIP_AcceptsPublic(t *testing.T) {
	cases := []string{
		"203.0.113.42", // TEST-NET-3 (public block, allowed by design)
		"198.51.100.7", // TEST-NET-2
		"192.0.2.5",    // TEST-NET-1
		"8.8.8.8",      // public DNS
		"1.1.1.1",      // public DNS
		"104.16.0.1",   // public CDN
	}
	for _, ip := range cases {
		t.Run(ip, func(t *testing.T) {
			parsed, err := netip.ParseAddr(ip)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := ValidateStaticEgressIP(parsed); err != nil {
				t.Errorf("ValidateStaticEgressIP(%s) = %v, want nil", ip, err)
			}
		})
	}
}

func TestValidateStaticEgressIP_RejectsReserved(t *testing.T) {
	cases := []struct {
		name string
		ip   string
	}{
		{"RFC1918-10", "10.1.2.3"},
		{"RFC1918-172.16", "172.16.5.6"},
		{"RFC1918-192.168", "192.168.1.1"},
		{"CGN", "100.64.0.1"},
		{"CGN-high", "100.127.255.254"},
		{"link-local", "169.254.1.1"},
		{"multicast", "224.0.0.1"},
		{"multicast-high", "239.255.255.255"},
		{"loopback", "127.0.0.1"},
		{"loopback-high", "127.255.255.254"},
		{"unspecified", "0.0.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := netip.ParseAddr(tc.ip)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			err = ValidateStaticEgressIP(parsed)
			if err == nil {
				t.Fatalf("ValidateStaticEgressIP(%s) = nil, want error", tc.ip)
			}
			if !IsStaticEgressIPError(err) {
				t.Errorf("ValidateStaticEgressIP(%s): err = %v, want IsStaticEgressIPError = true", tc.ip, err)
			}
		})
	}
}

func TestValidateStaticEgressIP_RejectsIPv6(t *testing.T) {
	parsed, _ := netip.ParseAddr("2001:db8::1")
	if err := ValidateStaticEgressIP(parsed); err == nil {
		t.Fatal("v6 must be rejected (deferred to follow-up ADR)")
	}
}

func TestValidateStaticEgressIP_RejectsInvalid(t *testing.T) {
	var invalid netip.Addr // zero value
	err := ValidateStaticEgressIP(invalid)
	if err == nil {
		t.Fatal("zero-value addr must be rejected")
	}
	if !IsStaticEgressIPError(err) {
		t.Errorf("zero-value rejection: IsStaticEgressIPError = false, err = %v", err)
	}
}

func TestStaticEgressIPDenyCIDRs_ReturnsCopy(t *testing.T) {
	a := StaticEgressIPDenyCIDRs()
	b := StaticEgressIPDenyCIDRs()
	if len(a) != len(b) || len(a) == 0 {
		t.Fatalf("len mismatch: %d vs %d", len(a), len(b))
	}
	// Mutate the returned slice; the package-private state must
	// not change.
	a[0] = netip.MustParsePrefix("0.0.0.0/0")
	c := StaticEgressIPDenyCIDRs()
	if c[0] == a[0] {
		t.Error("mutating returned slice leaked into package state")
	}
}
