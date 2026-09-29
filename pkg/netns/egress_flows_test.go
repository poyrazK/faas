// adr: 369 — egress flow log capture.
package netns

import (
	"net/netip"
	"reflect"
	"testing"
)

func TestParseEgressFlowSet(t *testing.T) {
	out := []byte(`{"nftables":[{"metainfo":{"version":"1.0.9"}},
		{"set":{"family":"ip","name":"egress_flows","table":"faas","type":["ipv4_addr","inet_service"],
		 "flags":["timeout","dynamic"],"timeout":600,"size":65535,
		 "elem":[{"elem":{"val":{"concat":["198.51.100.10",443]},"timeout":600,"expires":590}},
		         {"concat":["203.0.113.7",5432]},
		         {"elem":{"val":{"concat":["not-an-ip",443]}}},
		         {"elem":{"val":"198.51.100.1"}}]}}]}`)
	got, err := parseEgressFlowSet(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []EgressFlow{
		{Addr: netip.MustParseAddr("198.51.100.10"), Port: 443},
		{Addr: netip.MustParseAddr("203.0.113.7"), Port: 5432},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("flows = %+v, want %+v", got, want)
	}
	if empty, err := parseEgressFlowSet([]byte(`{"nftables":[{"set":{"name":"egress_flows"}}]}`)); err != nil || len(empty) != 0 {
		t.Fatalf("empty set = %v, %v", empty, err)
	}
	if _, err := parseEgressFlowSet([]byte("nope")); err == nil {
		t.Fatal("malformed output must be an error")
	}
}

// ADR-369: the flow set is declared in both families and recorded after
// the non-TCP drop and before the allowlist accept.
func TestTenantEgressFlowRecordRule(t *testing.T) {
	c := egressTestConfig()
	c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	lines := renderedLines(c)
	for _, tc := range []struct{ family, addrType string }{{"ip", "ipv4_addr"}, {"ip6", "ipv6_addr"}} {
		lineIndex(t, lines, "add set "+tc.family+" faas egress_flows { type "+tc.addrType+" . inet_service ; flags dynamic,timeout ; timeout 10m ; size 65535 ; }")
		record := lineIndex(t, lines, "rule "+tc.family+" faas forward iifname tap0 ct state new update @egress_flows { "+tc.family+" daddr . tcp dport }")
		nonTCP := lineIndex(t, lines, "rule "+tc.family+" faas forward iifname tap0 meta l4proto != tcp")
		if nonTCP >= record {
			t.Fatalf("%s: flow record rule (%d) must follow the non-TCP drop (%d)", tc.family, record, nonTCP)
		}
	}
	allow := lineIndex(t, lines, "rule ip faas forward iifname tap0 ip daddr { 1.2.3.0/24 }")
	if record := lineIndex(t, lines, "rule ip faas forward iifname tap0 ct state new update @egress_flows"); record >= allow {
		t.Fatalf("flow record rule (%d) must precede the allowlist accept (%d)", record, allow)
	}
}
