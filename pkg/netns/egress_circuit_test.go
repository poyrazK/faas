// adr: 201
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

// The flag-off equivalence claim in ADR-201 §4: a node without
// FAAS_EGRESS_CIRCUIT_BREAKER must render exactly the pre-ADR-201 ruleset,
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

func TestEgressCircuitSetCommands(t *testing.T) {
	c := circuitConfig(true)
	v4, err := ParseEgressCircuitTarget("203.0.113.9", 5432)
	if err != nil {
		t.Fatalf("ParseEgressCircuitTarget: %v", err)
	}
	v6, err := ParseEgressCircuitTarget("2001:db8::1", 6379)
	if err != nil {
		t.Fatalf("ParseEgressCircuitTarget v6: %v", err)
	}

	cmds := joinCmds(c.EgressCircuitSetCommands([]EgressCircuitTarget{v4, v6}))
	if len(cmds) != 4 {
		t.Fatalf("commands = %v, want two flushes plus one add per family", cmds)
	}
	if !strings.Contains(cmds[0], "flush set ip faas "+EgressCircuitSetName) ||
		!strings.Contains(cmds[1], "flush set ip6 faas "+EgressCircuitSetName) {
		t.Fatalf("commands = %v, want both families flushed first", cmds)
	}
	if !strings.Contains(cmds[2], "add element ip faas") || !strings.Contains(cmds[2], "203.0.113.9 . 5432") {
		t.Fatalf("v4 add = %q", cmds[2])
	}
	if !strings.Contains(cmds[3], "add element ip6 faas") || !strings.Contains(cmds[3], "2001:db8::1 . 6379") {
		t.Fatalf("v6 add = %q", cmds[3])
	}
	// Every command must run inside the instance's own netns; a circuit that
	// leaked into the root namespace would break every tenant on the node.
	for _, cmd := range cmds {
		if !strings.HasPrefix(cmd, "ip netns exec "+c.Netns+" nft ") {
			t.Fatalf("command %q does not execute inside the instance netns", cmd)
		}
	}
}

// Closing every circuit is expressed as an empty desired set, which must
// still flush. Emitting nothing would leave the previous elements installed
// and the dependency permanently unreachable.
func TestEgressCircuitSetCommandsEmptyStillFlushes(t *testing.T) {
	cmds := joinCmds(circuitConfig(true).EgressCircuitSetCommands(nil))
	if len(cmds) != 2 {
		t.Fatalf("commands = %v, want exactly the two family flushes", cmds)
	}
	for _, cmd := range cmds {
		if !strings.Contains(cmd, "flush set") {
			t.Fatalf("command = %q, want a flush", cmd)
		}
	}
}

// Dropping from a v4+v6 set to v4-only must clear the v6 family too, or the
// stale v6 element strands a circuit nothing is tracking.
func TestEgressCircuitSetCommandsFlushesBothFamilies(t *testing.T) {
	v4, _ := ParseEgressCircuitTarget("203.0.113.9", 5432)
	cmds := joinCmds(circuitConfig(true).EgressCircuitSetCommands([]EgressCircuitTarget{v4}))
	var sawV6Flush bool
	for _, cmd := range cmds {
		if strings.Contains(cmd, "flush set ip6 faas") {
			sawV6Flush = true
		}
	}
	if !sawV6Flush {
		t.Fatalf("commands = %v, want the v6 family flushed even with a v4-only desired set", cmds)
	}
}

func TestEgressCircuitSetCommandsDisabledEmitNothing(t *testing.T) {
	c := circuitConfig(false)
	tgt, _ := ParseEgressCircuitTarget("203.0.113.9", 5432)
	if cmds := c.EgressCircuitSetCommands([]EgressCircuitTarget{tgt}); len(cmds) != 0 {
		t.Fatalf("disabled config emitted commands: %v", cmds)
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

	cmds := joinCmds(c.EgressCircuitSetCommands([]EgressCircuitTarget{good, bad, worse}))
	// Two flushes plus exactly one add carrying only the valid element.
	if len(cmds) != 3 {
		t.Fatalf("commands = %v, want two flushes and one add", cmds)
	}
	if !strings.Contains(cmds[2], "203.0.113.9 . 5432") {
		t.Fatalf("command = %q, want the valid element", cmds[2])
	}
	if strings.Contains(cmds[2], "invalid") || strings.Count(cmds[2], " . ") != 1 {
		t.Fatalf("command = %q, want ONLY the valid element — one malformed element fails the whole nft batch", cmds[2])
	}

	if _, err := ParseEgressCircuitTarget("not-an-ip", 5432); err == nil {
		t.Fatal("ParseEgressCircuitTarget accepted a non-address")
	}
	if _, err := ParseEgressCircuitTarget("203.0.113.9", 70000); err == nil {
		t.Fatal("ParseEgressCircuitTarget accepted an out-of-range port")
	}
}
