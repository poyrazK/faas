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
	rule := lineIndex(t, lines, "add rule ip faas egress_account oifname vp0 ct direction original ip daddr { 10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10 } counter name faas_internal_tx")
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
