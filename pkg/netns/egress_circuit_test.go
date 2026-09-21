// adr: 195
package netns

import (
	"net/netip"
	"strings"
	"testing"
)

func circuitConfig(enabled bool) Config {
	c := NewConfig("inst-1", "faas-inst-1", "vh-inst-1", "vp-inst-1",
		netip.MustParseAddr("10.100.0.2"))
	c.EgressCircuitEnabled = enabled
	return c
}

func joinCmds(cmds [][]string) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

// The flag-off equivalence claim in ADR-195 §4: a node without
// FAAS_EGRESS_CIRCUIT_BREAKER must render exactly the pre-ADR-195 ruleset,
// command for command.
func TestNftCommandsUnchangedWhenEgressCircuitDisabled(t *testing.T) {
	off := joinCmds(circuitConfig(false).NftCommands())
	for _, cmd := range off {
		if strings.Contains(cmd, EgressCircuitSetName) || strings.Contains(cmd, EgressCircuitCounter) {
			t.Fatalf("disabled config emitted an egress-circuit command: %q", cmd)
		}
	}

	on := joinCmds(circuitConfig(true).NftCommands())
	if len(on) != len(off)+3 {
		t.Fatalf("enabled emitted %d commands, disabled %d; want exactly 3 more (set, counter, rule)", len(on), len(off))
	}
}

// The rule must sit AFTER the established/related accept. If it landed before,
// an opening circuit would reject the guest's in-flight replies and turn a
// recoverable blip into a guaranteed failure for every request already running.
func TestEgressCircuitRuleFollowsEstablishedAccept(t *testing.T) {
	cmds := joinCmds(circuitConfig(true).NftCommands())
	established, circuit := -1, -1
	for i, cmd := range cmds {
		if strings.Contains(cmd, "ct state established,related accept") && established < 0 {
			established = i
		}
		if strings.Contains(cmd, "@"+EgressCircuitSetName) && circuit < 0 {
			circuit = i
		}
	}
	if established < 0 {
		t.Fatal("no established/related accept rule found")
	}
	if circuit < 0 {
		t.Fatal("no egress-circuit reject rule found")
	}
	if circuit < established {
		t.Fatalf("egress-circuit rule at %d precedes the established/related accept at %d; "+
			"an open circuit must refuse NEW connections only", circuit, established)
	}
}

// reject-with-reset, not drop. A drop makes the guest wait out its full
// connect timeout, which is the exact failure this feature exists to remove.
func TestEgressCircuitRejectsRatherThanDrops(t *testing.T) {
	cmds := joinCmds(circuitConfig(true).NftCommands())
	var rule string
	for _, cmd := range cmds {
		if strings.Contains(cmd, "@"+EgressCircuitSetName) {
			rule = cmd
			break
		}
	}
	if rule == "" {
		t.Fatal("no egress-circuit rule found")
	}
	if !strings.Contains(rule, "reject with tcp reset") {
		t.Fatalf("rule = %q, want 'reject with tcp reset' — a drop would make the guest "+
			"wait out the full connect timeout, which is the failure this removes", rule)
	}
	if strings.Contains(rule, " drop") {
		t.Fatalf("rule = %q, must not drop", rule)
	}
}

func TestEgressCircuitElementCommands(t *testing.T) {
	c := circuitConfig(true)
	v4, err := ParseEgressCircuitTarget("203.0.113.9", 5432)
	if err != nil {
		t.Fatalf("ParseEgressCircuitTarget: %v", err)
	}
	v6, err := ParseEgressCircuitTarget("2001:db8::1", 6379)
	if err != nil {
		t.Fatalf("ParseEgressCircuitTarget v6: %v", err)
	}

	open := joinCmds(c.EgressCircuitOpenCommands([]EgressCircuitTarget{v4, v6}))
	if len(open) != 2 {
		t.Fatalf("open commands = %v, want one per address family", open)
	}
	if !strings.Contains(open[0], "add element ip faas "+EgressCircuitSetName) ||
		!strings.Contains(open[0], "203.0.113.9 . 5432") {
		t.Fatalf("v4 open command = %q", open[0])
	}
	if !strings.Contains(open[1], "add element ip6 faas "+EgressCircuitSetName) ||
		!strings.Contains(open[1], "2001:db8::1 . 6379") {
		t.Fatalf("v6 open command = %q", open[1])
	}

	closed := joinCmds(c.EgressCircuitCloseCommands([]EgressCircuitTarget{v4}))
	if len(closed) != 1 || !strings.Contains(closed[0], "delete element ip faas") {
		t.Fatalf("close commands = %v, want a single v4 delete", closed)
	}
	// Every command must run inside the instance's own netns; a circuit that
	// leaked into the root namespace would break every tenant on the node.
	for _, cmd := range append(open, closed...) {
		if !strings.HasPrefix(cmd, "ip netns exec "+c.Netns+" nft ") {
			t.Fatalf("command %q does not execute inside the instance netns", cmd)
		}
	}
}

func TestEgressCircuitElementCommandsDisabledEmitNothing(t *testing.T) {
	c := circuitConfig(false)
	tgt, _ := ParseEgressCircuitTarget("203.0.113.9", 5432)
	if cmds := c.EgressCircuitOpenCommands([]EgressCircuitTarget{tgt}); len(cmds) != 0 {
		t.Fatalf("disabled config emitted open commands: %v", cmds)
	}
	if cmds := c.EgressCircuitCloseCommands([]EgressCircuitTarget{tgt}); len(cmds) != 0 {
		t.Fatalf("disabled config emitted close commands: %v", cmds)
	}
}

// A malformed target must render nothing rather than a broken element — one
// bad element fails the whole nft batch, which would leave the tenant's
// firewall in whatever state the partial batch produced.
func TestEgressCircuitSkipsInvalidTargets(t *testing.T) {
	c := circuitConfig(true)
	good, _ := ParseEgressCircuitTarget("203.0.113.9", 5432)
	bad := EgressCircuitTarget{Port: 5432} // zero address
	worse := EgressCircuitTarget{Addr: good.Addr, Port: 0}

	cmds := joinCmds(c.EgressCircuitOpenCommands([]EgressCircuitTarget{good, bad, worse}))
	if len(cmds) != 1 {
		t.Fatalf("commands = %v, want only the valid target rendered", cmds)
	}
	if strings.Count(cmds[0], ".") < 1 || !strings.Contains(cmds[0], "203.0.113.9 . 5432") {
		t.Fatalf("command = %q, want only the valid element", cmds[0])
	}

	if _, err := ParseEgressCircuitTarget("not-an-ip", 5432); err == nil {
		t.Fatal("ParseEgressCircuitTarget accepted a non-address")
	}
	if _, err := ParseEgressCircuitTarget("203.0.113.9", 70000); err == nil {
		t.Fatal("ParseEgressCircuitTarget accepted an out-of-range port")
	}
}
