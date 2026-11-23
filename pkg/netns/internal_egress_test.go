package netns

import (
	"net/netip"
	"strings"
	"testing"
)

// R8-2: the ruleset counts the guest's own packets to internal destinations
// on a postrouting accounting chain, after every forward decision, by name.
func TestInternalEgressAccountingRendered(t *testing.T) {
	c := NewConfig("i", "fc-i", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
	lines := renderedLines(c)
	counter := lineIndex(t, lines, "add counter ip faas faas_internal_tx {}")
	chain := lineIndex(t, lines, "add chain ip faas egress_account { type filter hook postrouting priority 300 ; policy accept ; }")
	rule := lineIndex(t, lines, "add rule ip faas egress_account oifname vp0 ct direction original ip daddr { 10.0.0.0/8,100.64.0.0/10,172.16.0.0/12,192.168.0.0/16,198.18.0.0/15 } counter name faas_internal_tx")
	if !(counter < chain && chain < rule) {
		t.Fatalf("counter %d, chain %d, rule %d: the counter and chain must precede the rule", counter, chain, rule)
	}
	q := c
	q.QualificationOnly = true
	for _, line := range renderedLines(q) {
		if strings.Contains(line, "faas_internal_tx") {
			t.Fatalf("qualification-only netns renders internal accounting: %s", line)
		}
	}
}

// Service addresses and the database route sit in 198.18.0.0/15 and are
// translated on the host, so the netns chain sees them untranslated and must
// count them as internal. The operator's extra destinations (entry IPs, the
// managed database provider) join the set without overlapping elements.
func TestInternalEgressExtraDestinations(t *testing.T) {
	t.Cleanup(func() { SetInternalEgressExtraDestinations(nil) })
	SetInternalEgressExtraDestinations([]netip.Prefix{
		netip.MustParsePrefix("51.222.39.227/32"),
		netip.MustParsePrefix("51.222.39.228/32"),
		netip.MustParsePrefix("3.23.186.13/32"),
		netip.MustParsePrefix("10.1.2.3/32"),     // inside 10.0.0.0/8: dropped
		netip.MustParsePrefix("198.19.0.5/32"),   // inside 198.18.0.0/15: dropped
		netip.MustParsePrefix("2001:db8::1/128"), // IPv6: ignored
	})
	got := strings.Join(internalEgressDestinationList(), ",")
	want := "3.23.186.13/32,10.0.0.0/8,51.222.39.227/32,51.222.39.228/32,100.64.0.0/10,172.16.0.0/12,192.168.0.0/16,198.18.0.0/15"
	if got != want {
		t.Fatalf("destinations = %s\nwant %s", got, want)
	}
	c := NewConfig("i", "fc-i", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
	lineIndex(t, renderedLines(c), "add rule ip faas egress_account oifname vp0 ct direction original ip daddr { "+want+" } counter name faas_internal_tx")
}

// The parser keeps the internal counter's bytes next to its packets, and
// InternalEgressBytes adds the Ethernet framing the veth counter includes.
func TestInternalEgressBytesParsed(t *testing.T) {
	out := []byte(`{"nftables":[{"metainfo":{"version":"1.0.9"}},
{"counter":{"family":"ip","name":"faas_internal_tx","table":"faas","packets":10,"bytes":5000}},
{"counter":{"family":"ip","name":"deny_smtp","table":"faas","packets":3,"bytes":180}}]}`)
	counters, err := parseNftCounters(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := InternalEgressBytes(counters); got != 5000+10*EthernetHeaderBytes {
		t.Fatalf("InternalEgressBytes = %d, want %d", got, 5000+10*EthernetHeaderBytes)
	}
	if counters["deny_smtp"] != 3 || counters["deny_smtp"+CounterBytesSuffix] != 0 {
		t.Fatalf("deny counter = %v; packet counters keep packets only", counters)
	}
}
