// adr: 371 — egress flow log capture.
package netns

import (
	"net/netip"
	"reflect"
	"strings"
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

// ADR-371: the flow set is declared in both families and recorded after
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

func TestParseEgressBlockedSet(t *testing.T) {
	out := []byte(`{"nftables":[{"metainfo":{"version":"1.0.9"}},
		{"set":{"family":"ip","name":"egress_blocked","table":"faas","type":["ipv4_addr","inet_proto","inet_service"],
		 "flags":["timeout","dynamic"],"timeout":600,"size":65535,
		 "elem":[{"elem":{"val":{"concat":["10.0.0.5","udp",161]},"timeout":600,"expires":590}},
		         {"concat":["198.51.100.25","tcp",25]},
		         {"concat":["198.51.100.26",6,587]},
		         {"concat":["198.51.100.27","icmp",0]},
		         {"concat":["not-an-ip","tcp",25]}]}}]}`)
	got, err := parseEgressBlockedSet(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []EgressFlow{
		{Addr: netip.MustParseAddr("10.0.0.5"), Protocol: "udp", Port: 161},
		{Addr: netip.MustParseAddr("198.51.100.25"), Protocol: "tcp", Port: 25},
		{Addr: netip.MustParseAddr("198.51.100.26"), Protocol: "tcp", Port: 587},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blocked = %+v, want %+v", got, want)
	}
}

// Policy drops jump to egress_drop, which records guest TCP/UDP new flows
// in egress_blocked and always ends in an unconditional drop, so a goto is
// a drop verdict. Rate and flood limits keep dropping inline.
func TestTenantEgressBlockedRecordChain(t *testing.T) {
	c := egressTestConfig()
	c.DNSGated = true
	c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	lines := renderedLines(c)
	for _, tc := range []struct{ family, addrType string }{{"ip", "ipv4_addr"}, {"ip6", "ipv6_addr"}} {
		set := lineIndex(t, lines, "add set "+tc.family+" faas egress_blocked { type "+tc.addrType+" . inet_proto . inet_service ; flags dynamic,timeout ; timeout 10m ; size 4096 ; }")
		chain := lineIndex(t, lines, "add chain "+tc.family+" faas egress_drop")
		record := lineIndex(t, lines, "add rule "+tc.family+" faas egress_drop iifname tap0 meta l4proto { tcp,udp } ct state new limit rate 20/second burst 100 packets update @egress_blocked { "+tc.family+" daddr . meta l4proto . th dport }")
		var chainRules []string
		for _, line := range lines {
			if strings.Contains(line, "add rule "+tc.family+" faas egress_drop ") {
				chainRules = append(chainRules, line)
			}
		}
		if len(chainRules) != 2 || !strings.HasSuffix(chainRules[1], "add rule "+tc.family+" faas egress_drop drop") {
			t.Fatalf("%s egress_drop must be the record rule then an unconditional drop: %q", tc.family, chainRules)
		}
		forward := lineIndex(t, lines, "add chain "+tc.family+" faas forward")
		if set >= chain || chain >= record || record >= forward {
			t.Fatalf("%s: set (%d), chain (%d) and record rule (%d) must be declared before the forward chain (%d)", tc.family, set, chain, record, forward)
		}
		for _, fragments := range [][]string{
			{"daddr", "counter name drop_v", "goto egress_drop"},
			{"meta l4proto != tcp counter name faas_egress_denied goto egress_drop"},
			{"daddr != @egress_resolved counter name faas_egress_unresolved goto egress_drop"},
			{"tcp dport != @egress_ports counter name faas_egress_denied goto egress_drop"},
			{"iifname tap0 counter name deny_allowlist goto egress_drop"},
			{"limit rate over", "counter name faas_egress_rate drop"},
			{"counter name faas_egress_flood drop"},
		} {
			lineIndex(t, lines, append([]string{"rule " + tc.family + " faas forward "}, fragments...)...)
		}
	}
	lineIndex(t, lines, "rule ip faas forward iifname tap0 tcp dport { 25,465,587 } goto egress_drop")
	for _, line := range lines {
		if strings.Contains(line, " faas forward ") && strings.Contains(line, "@egress_blocked") {
			t.Fatalf("forward chain must not update egress_blocked directly: %q", line)
		}
	}
}
