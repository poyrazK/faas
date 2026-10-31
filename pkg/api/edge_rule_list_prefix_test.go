package api

import (
	"fmt"
	"net"
	"testing"
)

// adr: 091 — amendment 2026-10-31 (round 6): an IP list's CIDRs are looked up
// by prefix length, not scanned one by one. Membership is unchanged, an
// IPv4-mapped client address matches IPv4 CIDRs, and a list of 10,000 CIDRs
// answers with a handful of map lookups.
func TestEdgeRuleListIPPrefixLookup(t *testing.T) {
	items := []string{"10.0.0.0/8", "192.0.2.0/24", "203.0.113.7", "2001:db8::/32", "2001:db8:1::/48", "::ffff:198.51.100.0/120"}
	for i := range 10000 {
		items = append(items, fmt.Sprintf("100.%d.%d.0/24", i/256, i%256))
	}
	l, err := CompileEdgeRuleList(EdgeRuleListKindIP, items)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"10.255.0.1", true},
		{"11.0.0.1", false},
		{"192.0.2.200", true},
		{"192.0.3.1", false},
		{"203.0.113.7", true},
		{"203.0.113.8", false},
		{"2001:db8:ffff::1", true},
		{"2001:db9::1", false},
		{"::ffff:10.1.2.3", true},
		{"198.51.100.9", true},
		{"100.39.15.4", true},
		{"100.40.0.1", false},
	} {
		if got := l.containsIP(net.ParseIP(tc.ip)); got != tc.want {
			t.Errorf("containsIP(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
	if n := len(l.nets.v4.lengths) + len(l.nets.v6.lengths); n > 4 {
		t.Errorf("distinct prefix lengths = %d, want at most 4 (map lookups per address)", n)
	}
}
