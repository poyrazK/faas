// adr: 732
package netns

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func quarantineConfig(on bool) Config {
	c := NewConfig("inst-1", "faas-inst-1", "vh-inst-1", "vp-inst-1", netip.MustParseAddr("10.100.0.2"))
	c.Quarantine = on
	// Features that add accepts to the regular forward chain: quarantine
	// must hold regardless of them.
	c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("2001:db8::/32")}
	c.OperatorExceptions = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24")}
	return c
}

// A non-quarantined instance renders exactly the pre-ADR-732 ruleset.
func TestQuarantineOffRendersNothing(t *testing.T) {
	for _, cmd := range joinCmds(quarantineConfig(false).NftCommands()) {
		if strings.Contains(cmd, "quarantine") {
			t.Fatalf("non-quarantined config emitted %q", cmd)
		}
	}
}

// The quarantine lives in its own base chains in both families. A drop in
// any base chain is final, so nothing later added to the regular forward
// chain can override it.
func TestQuarantineRendersIsolatedBaseChainsPerFamily(t *testing.T) {
	cmds := joinCmds(quarantineConfig(true).NftCommands())
	for _, family := range []string{"ip", "ip6"} {
		pre := "ip netns exec faas-inst-1 nft add "
		want := []string{
			pre + "counter " + family + " faas quarantine_drop {}",
			pre + "chain " + family + " faas quarantine_forward { type filter hook forward priority -10 ; policy accept ; }",
			pre + "rule " + family + " faas quarantine_forward ct state established,related accept",
			pre + "rule " + family + " faas quarantine_forward iifname tap0 counter name quarantine_drop drop",
			pre + "rule " + family + " faas quarantine_forward oifname tap0 iifname != vp-inst-1 counter name quarantine_drop drop",
			pre + "chain " + family + " faas quarantine_input { type filter hook input priority -10 ; policy accept ; }",
			pre + "rule " + family + " faas quarantine_input ct state established,related accept",
			pre + "rule " + family + " faas quarantine_input iifname tap0 counter name quarantine_drop drop",
		}
		start := slices.Index(cmds, want[0])
		if start < 0 {
			t.Fatalf("%s: quarantine counter missing", family)
		}
		if got := cmds[start : start+len(want)]; !slices.Equal(got, want) {
			t.Fatalf("%s quarantine rules =\n%s\nwant\n%s", family, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}

// The established/related accept must precede the guest drop in the
// quarantine chain, or replies to platform requests (health checks,
// debugging traffic over VethPeer) would be dropped.
func TestQuarantineAcceptsRepliesBeforeDropping(t *testing.T) {
	cmds := joinCmds(quarantineConfig(true).NftCommands())
	established := slices.IndexFunc(cmds, func(s string) bool {
		return strings.HasSuffix(s, "quarantine_forward ct state established,related accept")
	})
	drop := slices.IndexFunc(cmds, func(s string) bool {
		return strings.HasSuffix(s, "quarantine_forward iifname tap0 counter name quarantine_drop drop")
	})
	if established < 0 || drop < 0 || established > drop {
		t.Fatalf("established accept at %d, guest drop at %d; want accept first", established, drop)
	}
}

// Quarantine must change the rendered ruleset, so anything keyed on it
// (the prepared-network pool, ADR-149) treats the two as different.
func TestQuarantineChangesTheRuleset(t *testing.T) {
	a, b := joinCmds(quarantineConfig(false).NftCommands()), joinCmds(quarantineConfig(true).NftCommands())
	if slices.Equal(a, b) {
		t.Fatal("quarantine did not change the rendered ruleset")
	}
}
